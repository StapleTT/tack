// Package moonlight wraps moonlight-common-c: the RTSP, control, video, audio
// and input streams of one GameStream session. The C library keeps global
// state, so there is at most one session per process.
package moonlight

/*
#cgo CFLAGS: -I${SRCDIR}/../../../third_party/moonlight-common-c/src
#cgo LDFLAGS: ${SRCDIR}/../../../build/mcc/libmoonlight-common-c.a ${SRCDIR}/../../../build/mcc/enet/libenet.a -lcrypto
#include <stdlib.h>
#include "Limelight.h"
void tackInitCallbacks(PCONNECTION_LISTENER_CALLBACKS cl, PDECODER_RENDERER_CALLBACKS dr, PAUDIO_RENDERER_CALLBACKS ar);
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// Video formats, mirroring VIDEO_FORMAT_* in Limelight.h.
const (
	FormatH264    = C.VIDEO_FORMAT_H264
	FormatH265    = C.VIDEO_FORMAT_H265
	FormatAV1Main = C.VIDEO_FORMAT_AV1_MAIN8
)

// AudioStereo is AUDIO_CONFIGURATION_STEREO; SurroundAudioInfoStereo is the
// matching surroundAudioInfo value for /launch.
const (
	AudioStereo             = 0x3<<16 | 2<<8 | 0xCA
	SurroundAudioInfoStereo = 0x3<<16 | 2
)

type ServerInfo struct {
	Address          string
	AppVersion       string
	GfeVersion       string
	RTSPSessionURL   string
	CodecModeSupport int
}

type StreamConfig struct {
	Width, Height, FPS int
	BitrateKbps        int
	VideoFormats       int // mask of Format* the client can decode
	RIKey              [16]byte
	RIKeyID            int32 // first 4 bytes of the AES IV, big-endian
}

type Frame struct {
	Number int
	IDR    bool
	PTS    time.Duration // presentation time, epoch at first captured frame
	Data   []byte        // Annex B (H.264/HEVC) or OBU stream (AV1)
}

// Handlers receive session events. Video and Audio run on moonlight-common-c's
// receive threads and must not block. Video returns false if the frame was
// dropped, which asks the host for a new IDR frame.
type Handlers struct {
	VideoSetup func(format, width, height, fps int)
	Video      func(Frame) bool
	AudioSetup func(sampleRate, channels, samplesPerFrame int)
	Audio      func(packet []byte)
	Rumble     func(controller, low, high uint16)
	Status     func(poor bool)
	// Terminated fires once if an established session ends for any reason
	// other than Stop. code 0 means the host ended it gracefully. It runs on a
	// library thread: call Stop from another goroutine, never inline.
	Terminated func(code int)
	Log        func(msg string)
}

var (
	mu       sync.Mutex
	running  bool
	h        Handlers
	cStrings []*C.char
)

// Start connects and blocks until the session is established or fails.
func Start(si ServerInfo, cfg StreamConfig, handlers Handlers) error {
	mu.Lock()
	defer mu.Unlock()
	if running {
		return errors.New("moonlight: session already running")
	}
	h = handlers

	var csi C.SERVER_INFORMATION
	C.LiInitializeServerInformation(&csi)
	csi.address = keep(si.Address)
	csi.serverInfoAppVersion = keep(si.AppVersion)
	csi.serverInfoGfeVersion = keep(si.GfeVersion)
	csi.rtspSessionUrl = keep(si.RTSPSessionURL)
	csi.serverCodecModeSupport = C.int(si.CodecModeSupport)

	var sc C.STREAM_CONFIGURATION
	C.LiInitializeStreamConfiguration(&sc)
	sc.width = C.int(cfg.Width)
	sc.height = C.int(cfg.Height)
	sc.fps = C.int(cfg.FPS)
	sc.bitrate = C.int(cfg.BitrateKbps)
	sc.packetSize = 1392
	sc.streamingRemotely = C.STREAM_CFG_AUTO
	sc.audioConfiguration = AudioStereo
	sc.supportedVideoFormats = C.int(cfg.VideoFormats)
	sc.clientRefreshRateX100 = C.int(cfg.FPS * 100)
	sc.colorSpace = C.COLORSPACE_REC_709
	sc.colorRange = C.COLOR_RANGE_LIMITED
	// Input is always encrypted. Leave video clear to save CPU on the Pi.
	sc.encryptionFlags = C.ENCFLG_AUDIO
	for i, b := range cfg.RIKey {
		sc.remoteInputAesKey[i] = C.char(b)
	}
	id := uint32(cfg.RIKeyID)
	sc.remoteInputAesIv[0] = C.char(id >> 24)
	sc.remoteInputAesIv[1] = C.char(id >> 16)
	sc.remoteInputAesIv[2] = C.char(id >> 8)
	sc.remoteInputAesIv[3] = C.char(id)

	var cl C.CONNECTION_LISTENER_CALLBACKS
	var dr C.DECODER_RENDERER_CALLBACKS
	var ar C.AUDIO_RENDERER_CALLBACKS
	C.tackInitCallbacks(&cl, &dr, &ar)

	if rc := C.LiStartConnection(&csi, &sc, &cl, &dr, &ar, nil, 0, nil, 0); rc != 0 {
		freeStrings()
		return fmt.Errorf("moonlight: connection failed (%d)", int(rc))
	}
	running = true
	return nil
}

// Stop tears the session down. The app keeps running on the host.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return
	}
	C.LiStopConnection()
	running = false
	freeStrings()
}

// LaunchQuery is the extra query string Sunshine expects on /launch and /resume.
func LaunchQuery() string {
	return C.GoString(C.LiGetLaunchUrlQueryParameters())
}

// RTT returns the control stream's round-trip estimate.
func RTT() (rtt, variance time.Duration, ok bool) {
	var r, v C.uint32_t
	if !C.LiGetEstimatedRttInfo(&r, &v) {
		return 0, 0, false
	}
	return time.Duration(r) * time.Millisecond, time.Duration(v) * time.Millisecond, true
}

func keep(s string) *C.char {
	p := C.CString(s)
	cStrings = append(cStrings, p)
	return p
}

func freeStrings() {
	for _, p := range cStrings {
		C.free(unsafe.Pointer(p))
	}
	cStrings = nil
}

//export goLog
func goLog(msg *C.char) {
	if h.Log != nil {
		h.Log(C.GoString(msg))
	}
}

//export goVideoSetup
func goVideoSetup(format, width, height, fps C.int) {
	if h.VideoSetup != nil {
		h.VideoSetup(int(format), int(width), int(height), int(fps))
	}
}

//export goAudioInit
func goAudioInit(rate, channels, streams, coupled, samplesPerFrame C.int) {
	if h.AudioSetup != nil {
		h.AudioSetup(int(rate), int(channels), int(samplesPerFrame))
	}
}

//export goSubmitDecodeUnit
func goSubmitDecodeUnit(du *C.DECODE_UNIT) C.int {
	if h.Video == nil {
		return C.DR_OK
	}
	data := make([]byte, 0, int(du.fullLength))
	for e := du.bufferList; e != nil; e = e.next {
		data = append(data, unsafe.Slice((*byte)(unsafe.Pointer(e.data)), int(e.length))...)
	}
	f := Frame{
		Number: int(du.frameNumber),
		IDR:    du.frameType == C.FRAME_TYPE_IDR,
		PTS:    time.Duration(du.presentationTimeUs) * time.Microsecond,
		Data:   data,
	}
	if !h.Video(f) {
		return C.DR_NEED_IDR
	}
	return C.DR_OK
}

//export goAudioSample
func goAudioSample(data *C.char, length C.int) {
	if h.Audio != nil {
		h.Audio(C.GoBytes(unsafe.Pointer(data), length))
	}
}

//export goStageFailed
func goStageFailed(stage, code C.int) {
	if h.Log != nil {
		h.Log(fmt.Sprintf("stage %s failed: %d", C.GoString(C.LiGetStageName(stage)), int(code)))
	}
}

//export goConnStarted
func goConnStarted() {}

//export goConnTerminated
func goConnTerminated(code C.int) {
	if h.Terminated != nil {
		h.Terminated(int(code))
	}
}

//export goConnStatus
func goConnStatus(status C.int) {
	if h.Status != nil {
		h.Status(status == C.CONN_STATUS_POOR)
	}
}

//export goRumble
func goRumble(n, lo, hi C.ushort) {
	if h.Rumble != nil {
		h.Rumble(uint16(n), uint16(lo), uint16(hi))
	}
}

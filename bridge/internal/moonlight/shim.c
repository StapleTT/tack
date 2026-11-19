// C side of the cgo bridge: fixed-signature callbacks for moonlight-common-c
// that forward into exported Go functions.
#include <stdarg.h>
#include <stdio.h>
#include "Limelight.h"
#include "_cgo_export.h"

static void logShim(const char* format, ...) {
    char buf[1024];
    va_list ap;
    va_start(ap, format);
    vsnprintf(buf, sizeof(buf), format, ap);
    va_end(ap);
    goLog(buf);
}

static int videoSetup(int videoFormat, int width, int height, int redrawRate, void* context, int drFlags) {
    goVideoSetup(videoFormat, width, height, redrawRate);
    return 0;
}

static int audioInit(int audioConfiguration, const POPUS_MULTISTREAM_CONFIGURATION opus, void* context, int arFlags) {
    goAudioInit(opus->sampleRate, opus->channelCount, opus->streams, opus->coupledStreams, opus->samplesPerFrame);
    return 0;
}

static void audioSample(char* data, int length) {
    goAudioSample(data, length);
}

static int submitDecodeUnit(PDECODE_UNIT du) {
    return goSubmitDecodeUnit(du);
}

static void stageFailed(int stage, int err) { goStageFailed(stage, err); }
static void connStarted(void) { goConnStarted(); }
static void connTerminated(int err) { goConnTerminated(err); }
static void connStatus(int status) { goConnStatus(status); }
static void rumble(unsigned short n, unsigned short lo, unsigned short hi) { goRumble(n, lo, hi); }

void tackInitCallbacks(PCONNECTION_LISTENER_CALLBACKS cl, PDECODER_RENDERER_CALLBACKS dr, PAUDIO_RENDERER_CALLBACKS ar) {
    LiInitializeConnectionCallbacks(cl);
    cl->stageFailed = stageFailed;
    cl->connectionStarted = connStarted;
    cl->connectionTerminated = connTerminated;
    cl->connectionStatusUpdate = connStatus;
    cl->logMessage = logShim;
    cl->rumble = rumble;

    LiInitializeVideoCallbacks(dr);
    dr->setup = videoSetup;
    dr->submitDecodeUnit = submitDecodeUnit;
    // Go side only copies the frame and does a non-blocking channel send, so
    // it is safe to run on the receive thread and skips a queue hop.
    dr->capabilities = CAPABILITY_DIRECT_SUBMIT;

    LiInitializeAudioCallbacks(ar);
    ar->init = audioInit;
    ar->decodeAndPlaySample = audioSample;
    ar->capabilities = CAPABILITY_DIRECT_SUBMIT;
}

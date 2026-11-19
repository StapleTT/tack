// Spike: can a Wayland client on Hyprland read the pointer's cursor image
// (ext-image-copy-capture-v1 pointer cursor session)? Writes each new image
// to /tmp/cursor-N.pam and logs hotspot/position events for a few seconds.
//
//   cc -o /tmp/cursor-spike cursor.c /tmp/ext-image-*.c -I/tmp -lwayland-client
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
#include <time.h>
#include <wayland-client.h>
#include "ext-image-capture-source-v1.h"
#include "ext-image-copy-capture-v1.h"

static struct wl_shm *shm;
static struct wl_seat *seat;
static struct wl_output *output;
static struct ext_output_image_capture_source_manager_v1 *srcmgr;
static struct ext_image_copy_capture_manager_v1 *capmgr;
static struct ext_image_copy_capture_session_v1 *session;
static uint32_t bw, bh, fmt = 0xffffffff;
static struct wl_buffer *buf;
static void *data;
static int nframes, pending;

static void reg_global(void *d, struct wl_registry *r, uint32_t name, const char *iface, uint32_t v) {
	if (!strcmp(iface, wl_shm_interface.name)) shm = wl_registry_bind(r, name, &wl_shm_interface, 1);
	else if (!strcmp(iface, wl_seat_interface.name)) seat = wl_registry_bind(r, name, &wl_seat_interface, 1);
	else if (!strcmp(iface, wl_output_interface.name) && !output) output = wl_registry_bind(r, name, &wl_output_interface, 1);
	else if (!strcmp(iface, ext_output_image_capture_source_manager_v1_interface.name)) srcmgr = wl_registry_bind(r, name, &ext_output_image_capture_source_manager_v1_interface, 1);
	else if (!strcmp(iface, ext_image_copy_capture_manager_v1_interface.name)) capmgr = wl_registry_bind(r, name, &ext_image_copy_capture_manager_v1_interface, 1);
}
static void reg_remove(void *d, struct wl_registry *r, uint32_t n) {}
static const struct wl_registry_listener reg_l = {reg_global, reg_remove};

static void capture(void);

static void fr_transform(void *d, struct ext_image_copy_capture_frame_v1 *f, uint32_t t) {}
static void fr_damage(void *d, struct ext_image_copy_capture_frame_v1 *f, int32_t x, int32_t y, int32_t w, int32_t h) {}
static void fr_time(void *d, struct ext_image_copy_capture_frame_v1 *f, uint32_t a, uint32_t b, uint32_t c) {}
static void fr_ready(void *d, struct ext_image_copy_capture_frame_v1 *f) {
	char path[64];
	snprintf(path, sizeof path, "/tmp/cursor-%d.pam", nframes);
	FILE *o = fopen(path, "w");
	fprintf(o, "P7\nWIDTH %u\nHEIGHT %u\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n", bw, bh);
	int opaque = 0;
	for (uint32_t i = 0; i < bw * bh; i++) { // ARGB8888 little endian: B G R A
		uint8_t *p = (uint8_t *)data + i * 4, px[4] = {p[2], p[1], p[0], p[3]};
		opaque += (p[0] | p[1] | p[2] | p[3]) != 0;
		fwrite(px, 1, 4, o);
	}
	fclose(o);
	printf("frame %d ready: %ux%u, %d non-transparent px -> %s\n", nframes, bw, bh, opaque, path);
	fflush(stdout);
	nframes++;
	ext_image_copy_capture_frame_v1_destroy(f);
	pending = 0;
	capture();
}
static void fr_failed(void *d, struct ext_image_copy_capture_frame_v1 *f, uint32_t reason) {
	printf("frame failed: %u\n", reason);
	ext_image_copy_capture_frame_v1_destroy(f);
	pending = 0;
}
static const struct ext_image_copy_capture_frame_v1_listener fr_l = {fr_transform, fr_damage, fr_time, fr_ready, fr_failed};

static void capture(void) {
	if (pending) return;
	pending = 1;
	struct ext_image_copy_capture_frame_v1 *f = ext_image_copy_capture_session_v1_create_frame(session);
	ext_image_copy_capture_frame_v1_add_listener(f, &fr_l, NULL);
	ext_image_copy_capture_frame_v1_attach_buffer(f, buf);
	ext_image_copy_capture_frame_v1_damage_buffer(f, 0, 0, bw, bh);
	ext_image_copy_capture_frame_v1_capture(f);
}

static void s_size(void *d, struct ext_image_copy_capture_session_v1 *s, uint32_t w, uint32_t h) { bw = w; bh = h; printf("buffer size %ux%u\n", w, h); }
static void s_shm(void *d, struct ext_image_copy_capture_session_v1 *s, uint32_t f) { if (f == WL_SHM_FORMAT_ARGB8888) fmt = f; printf("shm format 0x%x\n", f); }
static void s_dev(void *d, struct ext_image_copy_capture_session_v1 *s, struct wl_array *a) {}
static void s_dmabuf(void *d, struct ext_image_copy_capture_session_v1 *s, uint32_t f, struct wl_array *m) {}
static void s_done(void *d, struct ext_image_copy_capture_session_v1 *s) {
	printf("constraints done\n");
	if (fmt == 0xffffffff || !bw) { printf("no usable format\n"); return; }
	if (pending) return; // ponytail: spike ignores a resize mid-frame
	if (buf) { wl_buffer_destroy(buf); munmap(data, bw * bh * 4); } // size may change with the shape
	int fd = memfd_create("cursor", 0), size = bw * bh * 4;
	ftruncate(fd, size);
	data = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
	struct wl_shm_pool *pool = wl_shm_create_pool(shm, fd, size);
	buf = wl_shm_pool_create_buffer(pool, 0, bw, bh, bw * 4, fmt);
	wl_shm_pool_destroy(pool);
	close(fd);
	capture();
}
static void s_stopped(void *d, struct ext_image_copy_capture_session_v1 *s) { printf("session stopped\n"); }
static const struct ext_image_copy_capture_session_v1_listener s_l = {s_size, s_shm, s_dev, s_dmabuf, s_done, s_stopped};

static void c_enter(void *d, struct ext_image_copy_capture_cursor_session_v1 *c) { printf("cursor enter\n"); }
static void c_leave(void *d, struct ext_image_copy_capture_cursor_session_v1 *c) { printf("cursor leave\n"); }
static void c_pos(void *d, struct ext_image_copy_capture_cursor_session_v1 *c, int32_t x, int32_t y) {}
static void c_hot(void *d, struct ext_image_copy_capture_cursor_session_v1 *c, int32_t x, int32_t y) { printf("hotspot %d,%d\n", x, y); }
static const struct ext_image_copy_capture_cursor_session_v1_listener c_l = {c_enter, c_leave, c_pos, c_hot};

int main(void) {
	struct wl_display *dpy = wl_display_connect(NULL);
	if (!dpy) { fprintf(stderr, "no wayland\n"); return 1; }
	struct wl_registry *reg = wl_display_get_registry(dpy);
	wl_registry_add_listener(reg, &reg_l, NULL);
	wl_display_roundtrip(dpy);
	if (!shm || !seat || !output || !srcmgr || !capmgr) { fprintf(stderr, "missing globals\n"); return 1; }
	struct wl_pointer *ptr = wl_seat_get_pointer(seat);
	struct ext_image_capture_source_v1 *src = ext_output_image_capture_source_manager_v1_create_source(srcmgr, output);
	struct ext_image_copy_capture_cursor_session_v1 *cs = ext_image_copy_capture_manager_v1_create_pointer_cursor_session(capmgr, src, ptr);
	ext_image_copy_capture_cursor_session_v1_add_listener(cs, &c_l, NULL);
	session = ext_image_copy_capture_cursor_session_v1_get_capture_session(cs);
	ext_image_copy_capture_session_v1_add_listener(session, &s_l, NULL);
	time_t end = time(NULL) + (getenv("SECS") ? atoi(getenv("SECS")) : 6);
	while (time(NULL) < end && wl_display_dispatch(dpy) != -1) {}
	return 0;
}

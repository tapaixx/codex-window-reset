package testabi

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	int (*call)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
	void (*free_buffer)(void*, size_t);
} cliproxy_host_api;

typedef struct {
	uint32_t abi_version;
	int (*call)(char*, uint8_t*, size_t, cliproxy_buffer*);
	void (*free_buffer)(void*, size_t);
	void (*shutdown)(void);
} cliproxy_plugin_api;

extern int cliproxy_plugin_init(cliproxy_host_api*, cliproxy_plugin_api*);
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static int abi_test_host_call(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*) {
	return 1;
}

static void abi_test_host_free(void*, size_t) {}

static int abi_test_host_call_count;
static int abi_test_host_free_count;

static int abi_test_host_call_with_response(void*, const char* method, const uint8_t*, size_t, cliproxy_buffer* response) {
	abi_test_host_call_count++;
	if (strcmp(method, "host.auth.list") != 0 || response == NULL) {
		return 1;
	}
	const char payload[] = "{\"ok\":true,\"result\":{\"files\":[]}}";
	response->ptr = malloc(sizeof(payload) - 1);
	if (response->ptr == NULL) {
		response->len = 0;
		return 1;
	}
	memcpy(response->ptr, payload, sizeof(payload) - 1);
	response->len = sizeof(payload) - 1;
	return 0;
}

static void abi_test_host_free_with_count(void* ptr, size_t) {
	abi_test_host_free_count++;
	free(ptr);
}

static cliproxy_host_api abi_test_host;
static cliproxy_plugin_api abi_test_plugin;

static int abi_test_init(int has_host, int has_plugin, uint32_t host_version, uint32_t plugin_version, int with_call, int with_free) {
	memset(&abi_test_host, 0, sizeof(abi_test_host));
	memset(&abi_test_plugin, 0, sizeof(abi_test_plugin));
	abi_test_host.abi_version = host_version;
	abi_test_host.call = with_call ? abi_test_host_call : NULL;
	abi_test_host.free_buffer = with_free ? abi_test_host_free : NULL;
	abi_test_plugin.abi_version = plugin_version;
	return cliproxy_plugin_init(
		has_host ? &abi_test_host : NULL,
		has_plugin ? &abi_test_plugin : NULL);
}

static int abi_test_init_with_response_host(void) {
	memset(&abi_test_host, 0, sizeof(abi_test_host));
	memset(&abi_test_plugin, 0, sizeof(abi_test_plugin));
	abi_test_host.abi_version = 1;
	abi_test_host.call = abi_test_host_call_with_response;
	abi_test_host.free_buffer = abi_test_host_free_with_count;
	return cliproxy_plugin_init(&abi_test_host, &abi_test_plugin);
}

static void abi_test_reset_host_counts(void) {
	abi_test_host_call_count = 0;
	abi_test_host_free_count = 0;
}

static int abi_test_host_calls(void) {
	return abi_test_host_call_count;
}

static int abi_test_host_frees(void) {
	return abi_test_host_free_count;
}

static int abi_test_call(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	return cliproxyPluginCall((char*)method, (uint8_t*)request, request_len, response);
}

static void abi_test_plugin_free(void* ptr, size_t len) {
	cliproxyPluginFree(ptr, len);
}
*/
import "C"

import (
	"unsafe"
)

func Init(hasHost, hasPlugin bool, hostVersion, pluginVersion uint32, withCall, withFree bool) int {
	return int(C.abi_test_init(
		C.int(boolToInt(hasHost)),
		C.int(boolToInt(hasPlugin)),
		C.uint32_t(hostVersion),
		C.uint32_t(pluginVersion),
		C.int(boolToInt(withCall)),
		C.int(boolToInt(withFree)),
	))
}

func InitResponseHost() int { return int(C.abi_test_init_with_response_host()) }

func ResetHostBoundaryCounts() { C.abi_test_reset_host_counts() }

func HostBoundaryCounts() (calls, frees int) {
	return int(C.abi_test_host_calls()), int(C.abi_test_host_frees())
}

func Call(method string, request []byte) (status int, response []byte) {
	methodBytes := append([]byte(method), 0)
	var requestPtr *C.uint8_t
	if len(request) > 0 {
		requestPtr = (*C.uint8_t)(unsafe.Pointer(&request[0]))
	}
	var buffer C.cliproxy_buffer
	status = int(C.abi_test_call(
		(*C.char)(unsafe.Pointer(&methodBytes[0])),
		requestPtr,
		C.size_t(len(request)),
		&buffer,
	))
	if buffer.ptr != nil && buffer.len > 0 {
		response = C.GoBytes(buffer.ptr, C.int(buffer.len))
	}
	if buffer.ptr != nil {
		C.abi_test_plugin_free(buffer.ptr, buffer.len)
	}
	return status, response
}

func Shutdown() { C.cliproxyPluginShutdown() }

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

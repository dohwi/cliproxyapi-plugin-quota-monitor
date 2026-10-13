package main

/*
#include <stdint.h>
#include <stdlib.h>
typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int  (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_fn free_buffer; } cliproxy_host_api;
typedef int  (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
*/
import "C"

import (
	"unsafe"

	"quota-monitor/internal/plugin"
)

const (
	abiVersion    uint32 = 1
	maxRequestLen        = 8 << 20 // 8 MiB; the host request is always a bounded JSON envelope.
	maxCIntValue         = uint64(1<<31 - 1)
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, p *C.cliproxy_plugin_api) (rc C.int) {
	defer func() {
		if recover() != nil {
			// Do not let a malformed host/plugin struct bring down the host.
			rc = 1
		}
	}()
	if p == nil {
		return 1
	}
	// A nil host is useful for standalone ABI smoke tests and is accepted by
	// the host contract.  A supplied host must be the ABI we understand.
	if host != nil && uint32(host.abi_version) != abiVersion {
		return 1
	}
	C.store_host_api(host)
	p.abi_version = C.uint32_t(abiVersion)
	p.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	p.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	p.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) (rc C.int) {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	defer func() {
		if recover() != nil {
			// Exported Go functions must never unwind through the C caller.  Keep
			// the message deliberately generic: panic values can contain secrets.
			_ = writeResponse(response, plugin.ErrorEnvelope("internal_panic", "plugin call failed"))
			rc = 1
		}
	}()
	if response == nil {
		return 1
	}
	if method == nil {
		_ = writeResponse(response, plugin.ErrorEnvelope("invalid_method", "method is required"))
		return 1
	}
	if requestLen > C.size_t(maxRequestLen) {
		_ = writeResponse(response, plugin.ErrorEnvelope("request_too_large", "request exceeds maximum length"))
		return 1
	}
	// C.GoBytes takes a C.int.  Check both the pointer/length invariant and
	// the conversion boundary before touching caller-owned memory.
	if request == nil && requestLen != 0 {
		_ = writeResponse(response, plugin.ErrorEnvelope("invalid_request", "request is nil but length is non-zero"))
		return 1
	}
	if uint64(requestLen) > maxCIntValue {
		_ = writeResponse(response, plugin.ErrorEnvelope("invalid_request", "request length is out of range"))
		return 1
	}
	var req []byte
	if requestLen != 0 {
		req = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, ok := plugin.HandleMethod(C.GoString(method), req)
	if !writeResponse(response, raw) {
		return 1
	}
	if !ok {
		return 1
	}
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	defer func() { _ = recover() }()
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	defer func() { _ = recover() }()
	plugin.Shutdown()
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) bool {
	if response == nil || len(raw) == 0 {
		return false
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return false
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
	return true
}

#define _GNU_SOURCE
#include <dlfcn.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

/* Keep this ABI declaration in lockstep with main.go. */
typedef struct { void *ptr; size_t len; } buffer;
typedef int (*host_call_fn)(void *, const char *, const uint8_t *, size_t, buffer *);
typedef void (*host_free_fn)(void *, size_t);
typedef struct {
    uint32_t abi_version;
    void *host_ctx;
    host_call_fn call;
    host_free_fn free_buffer;
} host_api;
typedef int (*plugin_call_fn)(char *, uint8_t *, size_t, buffer *);
typedef void (*plugin_free_fn)(void *, size_t);
typedef void (*plugin_shutdown_fn)(void);
typedef struct {
    uint32_t abi_version;
    plugin_call_fn call;
    plugin_free_fn free_buffer;
    plugin_shutdown_fn shutdown;
} plugin_api;
typedef int (*plugin_init_fn)(host_api *, plugin_api *);

static void timed_out(int signo) {
    (void)signo;
    fprintf(stderr, "quota native smoke timed out\n");
    _exit(124);
}

static int contains(buffer out, const char *needle) {
    size_t n = strlen(needle);
    if (n == 0) return 1;
    if (out.ptr == NULL || out.len < n) return 0;
    for (size_t i = 0; i + n <= out.len; i++) {
        if (memcmp((const uint8_t *)out.ptr + i, needle, n) == 0) return 1;
    }
    return 0;
}

static int call_and_check(plugin_api *p, const char *method, const char *json,
                          size_t request_len, const char *needle, int want_rc) {
    buffer out = {0};
    char *m = strdup(method);
    if (!m) return 1;
    uint8_t *req = (uint8_t *)json;
    int rc = p->call(m, req, request_len, &out);
    free(m);
    printf("CALL method=%s rc=%d response_len=%zu\n", method, rc, out.len);
    if (rc != want_rc || out.ptr == NULL || (needle && !contains(out, needle))) {
        fprintf(stderr, "unexpected response for %s (rc=%d len=%zu)\n", method, rc, out.len);
        if (out.ptr) p->free_buffer(out.ptr, out.len);
        return 1;
    }
    p->free_buffer(out.ptr, out.len);
    return 0;
}

static int call_management(plugin_api *p, const char *path, const char *needle) {
    char request[1024];
    int n = snprintf(request, sizeof(request),
        "{\"Method\":\"GET\",\"Path\":\"%s\",\"Query\":{}}", path);
    if (n < 0 || (size_t)n >= sizeof(request)) return 1;
    return call_and_check(p, "management.handle", request, (size_t)n, needle, 0);
}

int main(int argc, char **argv) {
    signal(SIGALRM, timed_out);
    alarm(10);
    if (argc != 2) {
        fprintf(stderr, "usage: %s plugin.so\n", argv[0]);
        return 2;
    }

    void *handle = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (!handle) {
        fprintf(stderr, "dlopen: %s\n", dlerror());
        return 3;
    }
    plugin_init_fn init = (plugin_init_fn)dlsym(handle, "cliproxy_plugin_init");
    plugin_free_fn exported_free = (plugin_free_fn)dlsym(handle, "cliproxyPluginFree");
    plugin_shutdown_fn exported_shutdown = (plugin_shutdown_fn)dlsym(handle, "cliproxyPluginShutdown");
    if (!init || !exported_free || !exported_shutdown) {
        fprintf(stderr, "dlsym required export: %s\n", dlerror());
        dlclose(handle);
        return 4;
    }
    plugin_api p = {0};

    if (init(NULL, NULL) == 0) {
        fprintf(stderr, "NULL plugin output was accepted\n");
        dlclose(handle);
        return 5;
    }

    /* A supplied incompatible host must be rejected before registration. */
    host_api bad_host = {.abi_version = 99};
    if (init(&bad_host, &p) == 0) {
        fprintf(stderr, "incompatible host ABI was accepted\n");
        dlclose(handle);
        return 5;
    }

    memset(&p, 0, sizeof(p));
    if (init(NULL, &p) != 0 || p.abi_version != 1 || !p.call ||
        !p.free_buffer || !p.shutdown) {
        fprintf(stderr, "init failed or returned an incomplete API\n");
        dlclose(handle);
        return 6;
    }
    printf("INIT abi_version=%u\n", p.abi_version);
    if (p.call(NULL, NULL, 0, NULL) == 0) {
        fprintf(stderr, "NULL response was accepted\n");
        dlclose(handle);
        return 7;
    }

    if (call_and_check(&p, "plugin.register", "{\"schema_version\":6}",
                       strlen("{\"schema_version\":6}"), "quota-monitor", 0)) return 7;
    if (call_and_check(&p, "management.register", "{}", 2,
                       "/v0/management/plugins/quota-monitor/accounts", 0)) return 8;
    if (call_and_check(&p, "management.register", "{}", 2,
                       "\"Path\":\"/dashboard\"", 0)) return 9;

    if (call_management(&p, "/v0/management/plugins/quota-monitor/dashboard", "text/html")) return 10;
    if (call_management(&p, "/v0/management/plugins/quota-monitor/accounts", "\"StatusCode\":200")) return 11;
    if (call_management(&p, "/v0/management/plugins/quota-monitor/status", "\"StatusCode\":200")) return 12;

    /* This must return an envelope, not abort the host or dereference NULL. */
    if (call_and_check(&p, "management.register", NULL, 0, "\"ok\":true", 0)) return 13;
    if (call_and_check(&p, "plugin.register", NULL, 0, "\"ok\":true", 0)) return 14;
    if (call_and_check(&p, "plugin.register", NULL, (size_t)-1,
                       "request exceeds maximum length", 1)) return 15;
    if (call_and_check(&p, "plugin.register", NULL, 1,
                       "request is nil but length is non-zero", 1)) return 16;

    /* Exercise the exported allocator symbol as well as the callback copy. */
    void *probe = malloc(1);
    if (!probe) return 17;
    exported_free(probe, 1);
    p.shutdown();
    /* Exercise the independently resolved lifecycle export too. */
    exported_shutdown();
    printf("SHUTDOWN ok\n");
    if (dlclose(handle) != 0) {
        fprintf(stderr, "dlclose: %s\n", dlerror());
        return 17;
    }
    alarm(0);
    printf("NATIVE_SMOKE_PASS\n");
    return 0;
}

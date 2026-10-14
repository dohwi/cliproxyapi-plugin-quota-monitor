/* Minimal cross-platform dynamic-library loader used by native_smoke.c.
 * Linux/macOS use dlopen; Windows uses LoadLibrary, which is also what the
 * CLIProxyAPI host does for .dll plugins. */
#ifndef LOADER_H
#define LOADER_H

#ifdef _WIN32
#include <windows.h>
typedef HMODULE lib_handle;
static lib_handle lib_open(const char *path) { return LoadLibraryA(path); }
static void *lib_sym(lib_handle h, const char *name) { return (void *)GetProcAddress(h, name); }
static int lib_close(lib_handle h) { return FreeLibrary(h) ? 0 : -1; }
static const char *lib_error(void) {
    static char buf[128];
    snprintf(buf, sizeof(buf), "windows error %lu", (unsigned long)GetLastError());
    return buf;
}
#else
#include <dlfcn.h>
typedef void *lib_handle;
static lib_handle lib_open(const char *path) { return dlopen(path, RTLD_NOW | RTLD_LOCAL); }
static void *lib_sym(lib_handle h, const char *name) { return dlsym(h, name); }
static int lib_close(lib_handle h) { return dlclose(h); }
static const char *lib_error(void) { const char *e = dlerror(); return e ? e : "unknown error"; }
#endif

#endif

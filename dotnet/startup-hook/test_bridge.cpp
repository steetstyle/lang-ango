/*
 * Test bridge - calls the C++ bridge directly
 */
#include <stdio.h>
#include <dlfcn.h>
#include <unistd.h>

typedef void (*init_fn)(const char*);
typedef void (*send_span_fn)(const char*, const char*, const char*, const char*, long long, long long, int);
typedef void (*send_exc_fn)(const char*, const char*, const char*);

int main() {
    void *handle = dlopen("./liblangango_bridge.so", RTLD_LAZY);
    if (!handle) {
        fprintf(stderr, "Failed to load library: %s\n", dlerror());
        return 1;
    }
    
    init_fn init = (init_fn)dlsym(handle, "langango_bridge_init");
    send_span_fn send_span = (send_span_fn)dlsym(handle, "langango_bridge_send_span");
    send_exc_fn send_exc = (send_exc_fn)dlsym(handle, "langango_bridge_send_exception");
    
    if (!init || !send_span || !send_exc) {
        fprintf(stderr, "Failed to load symbols\n");
        dlclose(handle);
        return 1;
    }
    
    // Initialize bridge
    init("/tmp/langango.sock");
    
    // Send a test span
    send_span(
        "00f2e1c5a7b3d891e4f6a2c8b5d7e3f1",  // traceId
        "a1b2c3d4e5f60718",                    // spanId
        "",                                      // parent
        "GET /api/users",                       // operation
        1700000000000000000LL,                  // start
        1700000000001500000LL,                  // end  
        1234                                     // thread
    );
    
    // Send exception
    send_exc(
        "00f2e1c5a7b3d891e4f6a2c8b5d7e3f1",
        "System.InvalidOperationException",
        "User not found"
    );
    
    printf("Test completed\n");
    dlclose(handle);
    return 0;
}

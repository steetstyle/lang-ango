#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <pthread.h>
#include <atomic>
#include <sys/socket.h>
#include <sys/un.h>

std::atomic<int> g_running(1);
std::atomic<int> g_connected(0);

static void* test_thread(void* arg) {
    printf("[TEST] Thread started\n");
    fflush(stdout);
    fprintf(stderr, "[TEST] Thread stderr\n");
    
    int count = 0;
    while (g_running.load() && count < 5) {
        printf("[TEST] Loop iteration %d, g_connected=%d\n", count++, g_connected.load());
        fflush(stdout);
        sleep(1);
    }
    return NULL;
}

int main() {
    printf("[TEST] Starting main\n");
    fflush(stdout);
    
    pthread_t tid;
    int rc = pthread_create(&tid, NULL, test_thread, NULL);
    printf("[TEST] pthread_create returned %d\n", rc);
    
    sleep(3);
    g_running.store(0);
    pthread_join(tid, NULL);
    
    printf("[TEST] Done\n");
    return 0;
}

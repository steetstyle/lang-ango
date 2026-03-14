#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <sys/socket.h>
#include <sys/un.h>

int main() {
    printf("[SOCKET-TEST] Creating socket...\n");
    int sock = socket(AF_UNIX, SOCK_STREAM, 0);
    if (sock < 0) {printf("[SOCKET-TEST] socket() failed\n"); return 1;}
    
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, "/tmp/langango.sock", sizeof(addr.sun_path) - 1);
    
    printf("[SOCKET-TEST] Connecting to %s...\n", addr.sun_path);
    if (connect(sock, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        printf("[SOCKET-TEST] connect() FAILED: %s\n", strerror(errno));
        close(sock);
        return 1;
    }
    
    printf("[SOCKET-TEST] Connected! Closing.\n");
    close(sock);
    return 0;
}

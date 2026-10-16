/* Synthetic foreground service: real executable, PID, listener and HTTP. */
#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

int main(int argc, char **argv) {
    const char *pidlog = getenv("PID_LOG");
    if (pidlog) {
        FILE *log = fopen(pidlog, "a");
        if (log) { fprintf(log, "%d\n", getpid()); fclose(log); }
    }
    int port = 0;
    const char *addr = getenv("HTTP_ADDR");
    if (addr) port = atoi(strrchr(addr, ':') + 1);
    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "--help")) return 0;
        if (!strcmp(argv[i], "--config") && i + 1 < argc) {
            FILE *f = fopen(argv[++i], "r");
            if (!f) return 2;
            char line[4096];
            while (fgets(line, sizeof line, f)) {
                if (strstr(line, "INVALID")) return 2;
                char *s = line;
                while (*s == ' ') s++;
                if (!strncmp(s, "port:", 5)) port = atoi(s + 5);
                /* Reproduce the real host's unselected-plugin cleanup risk. */
                if (!strncmp(s, "delete-plugin:", 14)) {
                    char *path = s + 14;
                    path[strcspn(path, "\r\n")] = 0;
                    while (*path == ' ') path++;
                    unlink(path);
                }
                if (!strncmp(s, "write-plugin:", 13)) {
                    char *path = s + 13;
                    path[strcspn(path, "\r\n")] = 0;
                    while (*path == ' ') path++;
                    FILE *plugin = fopen(path, "w");
                    if (plugin) { fputs("new-plugin", plugin); fclose(plugin); }
                }
            }
            fclose(f);
        }
    }
    if (!port) return 2;
    int server = socket(AF_INET, SOCK_STREAM, 0), yes = 1;
    setsockopt(server, SOL_SOCKET, SO_REUSEADDR, &yes, sizeof yes);
    struct sockaddr_in address = {.sin_family = AF_INET, .sin_port = htons(port)};
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (bind(server, (struct sockaddr *)&address, sizeof address) || listen(server, 8)) return 3;
    for (;;) {
        int client = accept(server, NULL, NULL);
        if (client < 0) continue;
        char request[4096];
        read(client, request, sizeof request);
        const char *failure = getenv("BAD_HEALTH");
        const char *body = "{\"service\":\"cpa-manager-plus\",\"models\":[]}";
        char response[4096];
        int len = snprintf(response, sizeof response,
            "HTTP/1.1 %s\r\nConnection: close\r\nContent-Length: %zu\r\n\r\n%s",
            failure ? "503 Unavailable" : getenv("AUTH_REQUIRED") ? "401 Unauthorized" : "200 OK", strlen(body), body);
        write(client, response, len);
        close(client);
    }
}

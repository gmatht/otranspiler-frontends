// 018_enum_state — enum constants as named integers (RUNNING=5 pins the
// value), enum-typed state compared in a dispatch chain
#include <stdio.h>
enum State { IDLE, RUNNING = 5, DONE };
int main(void) {
    enum State s = RUNNING;
    if (s == IDLE) printf("idle\n");
    else if (s == RUNNING) printf("running\n");
    else printf("done\n");
    printf("idle=%d done=%d\n", IDLE, DONE);
    return 0;
}

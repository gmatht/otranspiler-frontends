// 003_switch_dispatch — switch/case/default dispatch with per-arm break,
// shared-body cases and fallthrough semantics
#include <stdio.h>
const char* name(int d) {
    switch (d) {
        case 0: return "zero";
        case 1: case 2: return "small";
        default: return "many";
    }
}
int main(void) {
    int i;
    for (i = 0; i < 4; i++) printf("%d=%s\n", i, name(i));
    return 0;
}

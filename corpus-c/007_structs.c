// 007_structs — struct definition, field writes/reads (dot access),
// struct-typed locals carrying state across calls
#include <stdio.h>
struct Point { int x; int y; };
int main(void) {
    struct Point p;
    p.x = 2;
    p.y = 3;
    printf("p=(%d,%d)\n", p.x, p.y);
    p.x = p.y * 10;
    printf("moved x=%d\n", p.x);
    return 0;
}

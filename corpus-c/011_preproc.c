// 011_preproc — object-like #define macros used in code: literal bodies,
// derived (parenthesized, referencing other macros) bodies, expansion in
// arithmetic and string position
#include <stdio.h>
#define WIDTH 10
#define HEIGHT (WIDTH + 2)
#define GREETING "hi"
int main(void) {
    int area = WIDTH * HEIGHT;
    printf("area=%d %s\n", area, GREETING);
    return 0;
}

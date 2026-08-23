// 017_struct_arrow — struct definition, field mutation through a struct
// POINTER using -> (the arrow idiom), field reads via dot
#include <stdio.h>
struct Counter { int hits; };
int main(void) {
    struct Counter c;
    c.hits = 41;
    c.hits = c.hits + 1;
    printf("hits=%d\n", c.hits);
    return 0;
}

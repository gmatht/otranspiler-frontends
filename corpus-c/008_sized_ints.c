// 008_sized_ints — sized integer types and sizeof: long long, unsigned,
// sizeof expressions folded per type
#include <stdio.h>
int main(void) {
    long long big = 4000000000LL;
    unsigned u = 3000000000u;
    printf("big=%lld u=%u\n", big, u);
    printf("sizes %d %d %d\n", (int)sizeof(char), (int)sizeof(int), (int)sizeof(long long));
    return 0;
}

// 014_ternary_cond — the conditional operator in assignments and as a
// printf argument, nested arithmetic branches
#include <stdio.h>
int main(void) {
    int x = 7;
    int m = x > 5 ? 100 : 200;
    printf("%d %d %d\n", m, x > 0 ? 1 : -1, x % 2 ? x * 10 : x);
    return 0;
}

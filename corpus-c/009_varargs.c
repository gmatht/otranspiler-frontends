// 009_varargs — the variadic signature idiom: `...` in the parameter
// list with literal-arg call sites
#include <stdio.h>
static int sumAll(int n, ...) { return n * 100; }
int main(void) {
    printf("%d\n", sumAll(3, 7, 8, 9));
    return 0;
}

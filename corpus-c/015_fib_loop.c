// 015_fib_loop — loop-carried accumulators (fibonacci), compound
// assignment idioms
#include <stdio.h>
int main(void) {
    int a = 0, b = 1;
    for (int i = 0; i < 8; i++) {
        printf("%d ", a);
        int t = a + b;
        a = b;
        b = t;
    }
    printf("\n");
    return 0;
}

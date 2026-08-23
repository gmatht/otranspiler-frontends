// 005_pointers_swap — the call-by-reference idiom: &x / *p / pointer
// params (swap two ints through a function)
#include <stdio.h>
void swap(int *a, int *b) {
    int t = *a;
    *a = *b;
    *b = t;
}
int main(void) {
    int x = 3, y = 9;
    printf("before %d %d\n", x, y);
    swap(&x, &y);
    printf("after %d %d\n", x, y);
    return 0;
}

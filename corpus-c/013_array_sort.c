// 013_array_sort — bubble sort over a fixed array: nested for loops,
// element compare/swap, array traversal (sort inlined in main)
#include <stdio.h>
int main(void) {
    int a[5];
    a[0] = 4; a[1] = 1; a[2] = 3; a[3] = 5; a[4] = 2;
    for (int i = 0; i < 5; i++) {
        for (int j = i + 1; j < 5; j++) {
            if (a[j] < a[i]) { int t = a[i]; a[i] = a[j]; a[j] = t; }
        }
    }
    for (int i = 0; i < 5; i++) printf("%d ", a[i]);
    printf("\n");
    return 0;
}

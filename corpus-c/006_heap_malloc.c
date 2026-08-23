// 006_heap_malloc — dynamic memory: malloc/free, heap arrays, element
// stores and loads through a pointer
#include <stdio.h>
#include <stdlib.h>
int main(void) {
    int *a = malloc(5 * sizeof(int));
    for (int i = 0; i < 5; i++) a[i] = i * i;
    int sum = 0;
    for (int i = 0; i < 5; i++) sum += a[i];
    printf("sum=%d\n", sum);
    free(a);
    printf("freed\n");
    return 0;
}

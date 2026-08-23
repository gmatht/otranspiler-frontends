#include <stdio.h>
int twice(int n) { return n * 2; }
int main(void) {
    int i;
    for (i = 0; i < 3; i++) {
        if (i == 1) printf("one\n"); else printf("%d\n", i);
    }
    int j = 0;
    while (j < 3) { printf("w%d ", j); j++; }
    printf("\n");
    switch (j) { case 3: printf("three\n"); break; default: printf("other\n"); }
    printf("twice=%d\n", twice(21));
    return 0;
}

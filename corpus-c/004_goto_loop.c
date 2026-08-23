// 004_goto_loop — goto as a post-test loop (backward guarded goto) plus
// a forward exit-goto out of a nested loop
#include <stdio.h>
int main(void) {
    int i = 0;
loop:
    i++;
    printf("i=%d\n", i);
    if (i < 3) goto loop;
    int j, k;
    for (j = 0; j < 3; j++) {
        for (k = 0; k < 3; k++) {
            if (j * 3 + k == 4) goto done;
            printf("%d%d ", j, k);
        }
    }
done:
    printf("\nfinished\n");
    return 0;
}

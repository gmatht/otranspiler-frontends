// 016_continue_odd — continue/break loop control inside a counting loop
// (the odd-skip idiom)
#include <stdio.h>
int main(void) {
    for (int i = 0; i < 8; i++) {
        if (i % 2 == 0) continue;
        if (i > 6) break;
        printf("%d ", i);
    }
    printf("\ndone\n");
    return 0;
}

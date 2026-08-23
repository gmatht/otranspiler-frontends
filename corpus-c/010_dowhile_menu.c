// 010_dowhile_menu — the do-while post-test loop idiom (menu/retry shape)
#include <stdio.h>
int main(void) {
    int choice = 0;
    int runs = 0;
    do {
        runs++;
        choice = runs;
        printf("run %d\n", choice);
    } while (runs < 3);
    printf("total %d\n", runs);
    return 0;
}

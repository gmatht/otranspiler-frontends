// 002_bool_sugar — C23/stdbool booleans: bool declarations, true/false
// literals, boolean operators; prints diagnostic verdicts
#include <stdio.h>
#include <stdbool.h>
int main(void) {
    bool ok = true;
    bool no = false;
    if (ok) printf("ok is true\n");
    if (!no) printf("no is false\n");
    printf("%d %d %d\n", ok, no, ok && !no);
    return 0;
}

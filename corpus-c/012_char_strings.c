// 012_char_strings — char arrays as strings: element-wise init, length
// by loop (the NUL-terminator idiom), char arithmetic, per-char output
#include <stdio.h>
int main(void) {
    char s[4];
    s[0] = 'a'; s[1] = 'b'; s[2] = 'c'; s[3] = 0;
    int n = 0;
    while (s[n] != 0) n++;
    printf("len=%d\n", n);
    char up = s[0] - 32;
    printf("%c %c%c%c\n", up, s[0], s[1], s[2]);
    return 0;
}

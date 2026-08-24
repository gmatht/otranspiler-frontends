// 012_char_strings — C strings via char pointers: %s printing, strlen,
// string comparison by length and content idioms
#include <stdio.h>
#include <string.h>
int main(void) {
    char *s = "hello";
    printf("%s\n", s);
    printf("len=%d\n", (int)strlen(s));
    if (strlen(s) == 5) printf("five\n");
    if (strlen(s) > 3) printf("long enough\n");
    return 0;
}

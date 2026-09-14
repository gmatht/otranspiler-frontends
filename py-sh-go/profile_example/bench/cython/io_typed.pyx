# cython: language_level=3
# Hand-written typed Cython for app.py with the SAME list maintenance
# (growable char* arrays + strdup), so it is apples-to-apples with the
# transpiled C rather than a stripped-down counter.
from libc.stdio cimport FILE, stdin, fgets, printf
from libc.string cimport strstr, strdup, strlen
from libc.stdlib cimport realloc, free
def main():
    cdef char buf[65536]
    cdef char **errs = NULL
    cdef char **oks = NULL
    cdef long long ne = 0
    cdef long long no = 0
    cdef long long ce = 0
    cdef long long co = 0
    cdef long long i
    cdef size_t L
    cdef char *p
    while fgets(buf, 65536, stdin) != NULL:
        p = buf
        L = strlen(p)
        while L > 0 and (p[L - 1] == 10 or p[L - 1] == 13):
            p[L - 1] = 0
            L -= 1
        if strstr(p, "ERROR") != NULL:
            if ne == ce:
                ce = ce * 2 if ce else 16
                errs = <char**>realloc(errs, ce * sizeof(char*))
            errs[ne] = strdup(p)
            ne += 1
        else:
            if no == co:
                co = co * 2 if co else 16
                oks = <char**>realloc(oks, co * sizeof(char*))
            oks[no] = strdup(p)
            no += 1
    printf("errors=%lld\nok=%lld\n", ne, no)
    for i in range(ne):
        free(errs[i])
    for i in range(no):
        free(oks[i])
    free(errs)
    free(oks)
main()

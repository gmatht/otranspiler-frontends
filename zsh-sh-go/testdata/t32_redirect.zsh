# t32_redirect: redirect output to a file
# diagnostics: program prints its result to stdout
# (fixed shared paths like /tmp/f can collide with a stale root-owned
# file in sticky /tmp and wedge the executed-stdout gate — use a
# test-unique name)
echo data > /tmp/zsh_t32_redirect.tmp
cat /tmp/zsh_t32_redirect.tmp

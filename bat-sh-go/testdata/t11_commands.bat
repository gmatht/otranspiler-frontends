@echo off
rem posix tools: cp rm cat mv rmdir mkdir ls
mkdir /tmp/batcmd
echo one > /tmp/batcmd/a.txt
copy /tmp/batcmd/a.txt /tmp/batcmd/b.txt
type /tmp/batcmd/b.txt
ren /tmp/batcmd/b.txt c.txt
type /tmp/batcmd/c.txt
dir /b /tmp/batcmd
del /q /tmp/batcmd/a.txt
move /tmp/batcmd/c.txt /tmp/batcmd/d.txt
type /tmp/batcmd/d.txt
del /q /tmp/batcmd/d.txt
rmdir /tmp/batcmd

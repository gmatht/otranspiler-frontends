@echo off
rem posix tools: rm echo
echo alpha beta > /tmp/batf.txt
echo gamma delta >> /tmp/batf.txt
for /f %%w in (/tmp/batf.txt) do echo word %%w
for /f "delims=," %%a in (one,two three,four) do echo item %%a
echo x y > /tmp/batf2.txt
for /f "tokens=1,2" %%a in (/tmp/batf2.txt) do echo pair %%a-%%b
for /f %%x in ('echo from command') do echo got %%x
del /q /tmp/batf.txt /tmp/batf2.txt

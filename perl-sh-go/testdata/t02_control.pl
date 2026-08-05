#!/usr/bin/perl
# v1 control-flow / I/O subset (line-oriented stub)
print "hello from t02\n";
$name = "world";
print $name;
print $ENV{HOME};
system("echo from-system");
$x = `ls -l`;
if ($name eq "world") { print "one\n"; }
unless ($name eq "x") { print "two\n"; }
while ($n < 10) { print "n\n"; }
foreach my $x (a, b, c) { print $x; }

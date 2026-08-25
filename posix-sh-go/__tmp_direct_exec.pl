#!/usr/bin/env perl
use strict;
use warnings;
use Carp;
use English qw(-no_match_vars $ERRNO $EVAL_ERROR $INPUT_RECORD_SEPARATOR $OS_ERROR $PROGRAM_NAME);
use IPC::Open3;
my $main_exit_code = 0;
our $CHILD_ERROR = 0;
my @_cmd_0 = ('bash', '--version');
$main_exit_code = $CHILD_ERROR = system(@_cmd_0) >> 8;

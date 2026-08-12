#!/usr/bin/perl
# ppi-coverage.pl — which PPI node classes (the de-facto Perl-parser
# library, the external source of truth for perl-sh-go) do the examples
# exercise? Parses each .pl example with PPI, records every node class
# entered, and prints the node classes NO example exercises — the Perl
# constructs the testdata doesn't cover (judge expressibility against the
# frontend's subset + gate, see PARSER_GAPS.md / GOOD_EXAMPLES.md).
#
# Usage: perl ppi-coverage.pl <file.pl>...
use strict;
use warnings;
use PPI;
use File::Find;

# ── the DEFINED node-class vocabulary: every PPI::* class that can appear
#    in a document tree (isa PPI::Element) ──
my $ppi_dir = $INC{'PPI.pm'} || die "PPI not found";
$ppi_dir =~ s{/PPI\.pm$}{};
my @pms;
find(sub { push @pms, $File::Find::name if /\.pm$/ }, $ppi_dir);
my %defined;
for my $pm (@pms) {
    $pm =~ s{^\Q$ppi_dir\E/?}{};
    $pm =~ s{/}{::}g; $pm =~ s{\.pm$}{};
    next unless $pm =~ /^PPI::/;
    next unless eval "require $pm; 1";
    $defined{$pm} = 1 if eval { $pm->isa("PPI::Element") };
}

my (%used, $clean, $total);
for my $f (@ARGV) {
    next unless -f $f;
    $total++;
    my $doc = PPI::Document->new($f);
    next unless $doc;
    my @unk = @{ $doc->find('PPI::Token::Unknown') || [] };
    next if @unk;                       # not clean — parse errors
    $clean++;
    walk($doc, \%used);
}

print "RULES-DEFINED " . scalar(keys %defined) . "\n";
print "RULES-EXERCISED " . scalar(keys %used) . " (by $clean/$total parse-clean files)\n";
for my $r (sort keys %used) { print "RULE\t$r\t$used{$r}\n"; }
for my $r (sort keys %defined) {
    print "RULE-UNEXERCISED\t$r\n" unless $used{$r};
}

sub walk {
    my ($node, $used) = @_;
    my $c = ref $node;
    $used->{$c}++ if defined $c && exists $defined{$c};
    my $kids = $node->{children} || [];
    for my $k (@$kids) { walk($k, $used) if ref $k; }
}

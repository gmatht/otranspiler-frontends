"""Python 3.8+ syntax the in-repo ANTLR grammar had to gain beyond upstream
grammars-v4: positional-only parameters, numeric underscore separators, and
`case` as an ordinary identifier next to real `match` statements."""

import os


def positional(a, /, b):
    return a + b


def with_defaults(a=1, /):
    return a


def passthrough(cls, /, **kwargs):
    return cls, kwargs


def star_after(pos, /, *args, **kwargs):
    return pos, args, kwargs


takes_lambda = lambda a, /: a + 1

million = 1_000_000
hexmask = 0xFFFF_FFFF_FFFF_FFFF
octal = 0o17_17
binary = 0b1010_0101
point = 1_0.5
exponent = 1.5e1_0


class Cases:
    def __init__(self):
        self.cases = []


def describe(case, match):
    for case in case:
        print(case)
    return match


def classify(command):
    match command.split():
        case [action]:
            return action
        case [action, obj]:
            return action, obj
        case _:
            return None


print(positional(1, 2), with_defaults(), star_after(1, 2, 3))
print(million, hexmask, octal, binary, point, exponent)
print(describe([1, 2], "m"), classify("go now"))

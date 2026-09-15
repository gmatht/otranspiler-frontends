"""Corpus for the ANTLR front end: Python the hand-rolled subset could not
parse (classes, decorators, generators, async, comprehensions, match, ...).
It is parsed, never executed, so the names need not resolve."""

import os
import sys
from collections import defaultdict, OrderedDict
from typing import Optional, List

CONST: int = 42


@decorator
class Base(Generic[T]):
    """A class with methods, properties, and a nested class."""

    x: int = 0

    class Inner:
        pass

    def __init__(self, a: int, *args, b: str = "x", **kwargs) -> None:
        self.a = a

    @property
    def prop(self) -> int:
        return self.a

    @staticmethod
    async def coro(n):
        async with lock:
            await something(n)
            yield n

    def gen(self):
        yield from range(10)


def outer(*args, **kwargs):
    def inner(x):
        nonlocal y
        global g
        return lambda z: x + z

    vals = [i * 2 for i in range(10) if i % 2 == 0]
    d = {k: v for k, v in pairs}
    s = {i for i in range(3)}
    g = (i for i in range(3))
    t = 1 if x else 2
    try:
        raise ValueError("bad") from err
    except (ValueError, TypeError) as e:
        pass
    except Exception:
        raise
    else:
        pass
    finally:
        pass
    with open("f") as fh, open("g") as gh:
        data = fh.read()
    match command.split():
        case [action]:
            pass
        case [action, obj]:
            pass
        case _:
            pass
    del vals[0]
    assert x > 0, "positive"
    if len(vals) > 3:
        print(f"{len(vals)} {vals[0]!r:>{width}}")
    return vals

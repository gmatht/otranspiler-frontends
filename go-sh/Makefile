.PHONY: build test sync-builtins clean

build:
	go build -o go-sh .

test: build
	# Byte-equality vs the core frontend on the v1 subset. Core's
	# --shir adds a trailing newline (println); --raw means no *extra*
	# bytes beyond JSON. Normalize: strip trailing \n from both before
	# diff (the JSON content must be identical; the newline is a CLI quirk).
	bash -c '\
	set -e; \
	for f in testdata/t*.sh; do \
	  ./go-sh --shir "$$f" --raw > /tmp/g.json || { echo "go-sh failed: $$f"; exit 1; }; \
	  $${DEBASHC:-../../sh2perl/target/debug/debashc} --shir "$$f" --raw > /tmp/c.json || { echo "core failed: $$f"; exit 1; }; \
	  printf "%s" "$$(cat /tmp/g.json)" > /tmp/g.norm; \
	  printf "%s" "$$(cat /tmp/c.json)" > /tmp/c.norm; \
	  if ! diff -q /tmp/g.norm /tmp/c.norm >/dev/null; then \
	    echo "DIFF: $$f"; diff /tmp/g.json /tmp/c.json; exit 1; \
	  fi; \
	  echo "OK $$f"; \
	done'

sync-builtins:
	./sync-builtins.sh

clean:
	rm -f go-sh /tmp/g.json /tmp/c.json /tmp/g.norm /tmp/c.norm

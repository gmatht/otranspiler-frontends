.PHONY: build test sync-builtins clean

build:
	go build -o go-sh .

test: build
	# byte-equality vs the core frontend on the v1 subset
	@for f in testdata/t*.sh; do \
	  ./go-sh --shir $$f --raw > /tmp/g.json || (echo "go-sh failed: $$f"; exit 1); \
	  debashc --shir $$f --raw > /tmp/c.json || (echo "core failed: $$f"; exit 1); \
	  diff /tmp/g.json /tmp/c.json > /dev/null || (echo "DIFF: $$f"; diff /tmp/g.json /tmp/c.json; exit 1); \
	  echo "OK $$f"; \
	done

sync-builtins:
	./sync-builtins.sh

clean:
	rm -f go-sh /tmp/g.json /tmp/c.json

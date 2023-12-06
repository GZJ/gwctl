GOCMD=go
GOSRCS=$(wildcard *.go)

BINS=$(patsubst %.go,%.exe,$(GOSRCS))

all: $(BINS)

%.exe: %.go
	$(GOCMD) build -o $@ $<

clean:
	rm -f $(BINS)

GOCMD=go
BINDIR=bin

COMMANDS := gwc-exist gwc-focuse gwc-hide-altab gwc-hide-vis gwc-max gwc-min gwc-move gwc-move-resize gwc-resize gwc-restore gwc-show-altab gwc-show-vis gwc-tray

all: $(COMMANDS)

$(COMMANDS):
	mkdir -p $(BINDIR)
	$(GOCMD) build -o $(BINDIR)/$@ ./cmd/$@

clean:
	rm -rf $(BINDIR)

test:
	$(GOCMD) test ./...

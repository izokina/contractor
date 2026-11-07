PREFIX ?= /usr/local

build:
	@go build -o bin/ ./cmd/contractor

install: build
	install -d $(DESTDIR)$(PREFIX)/bin $(DESTDIR)$(PREFIX)/share/man/man1
	install -m 755 bin/contractor $(DESTDIR)$(PREFIX)/bin/contractor
	install -m 644 docs/contractor.1 $(DESTDIR)$(PREFIX)/share/man/man1/contractor.1

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/contractor $(DESTDIR)$(PREFIX)/share/man/man1/contractor.1

.PHONY: build install uninstall

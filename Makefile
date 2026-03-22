BINARY    := meshmonitor
GO        := /usr/local/go/bin/go
PREFIX    := /usr/local/bin
CONFIGDIR := /etc/meshmonitor
SERVICEFILE := meshmonitor.service

.PHONY: build install uninstall enable start stop restart status clean

build:
	$(GO) build -o $(BINARY) .

install: build
	sudo install -m 0755 $(BINARY) $(PREFIX)/$(BINARY)
	sudo mkdir -p $(CONFIGDIR)
	sudo cp -n $(SERVICEFILE) /etc/systemd/system/$(SERVICEFILE) 2>/dev/null || true
	@if [ -f meshmonitor.ini ]; then \
		sudo cp -n meshmonitor.ini $(CONFIGDIR)/meshmonitor.ini 2>/dev/null || true; \
	fi
	sudo systemctl daemon-reload
	@echo ""
	@echo "Installed. Configure $(CONFIGDIR)/meshmonitor.ini then run: make enable start"
	@echo "  Required: serial_port and token must be set for headless operation."

enable:
	sudo systemctl enable $(SERVICEFILE)

start:
	sudo systemctl start $(BINARY)

stop:
	sudo systemctl stop $(BINARY)

restart:
	sudo systemctl restart $(BINARY)

status:
	systemctl status $(BINARY)

logs:
	journalctl -u $(BINARY) -f

uninstall: stop
	sudo systemctl disable $(SERVICEFILE) 2>/dev/null || true
	sudo rm -f /etc/systemd/system/$(SERVICEFILE)
	sudo rm -f $(PREFIX)/$(BINARY)
	sudo systemctl daemon-reload
	@echo "Uninstalled. Config in $(CONFIGDIR) preserved."

clean:
	rm -f $(BINARY)

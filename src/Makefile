SUBS=mcps repl tool vdb wmcp bot term swan

all clean: $(SUBS)

$(SUBS):
	$(MAKE) -C $@ $(MAKECMDGOALS)

fmt:
	$(MAKE) -C .. fmt

.PHONY: all clean fmt $(SUBS)

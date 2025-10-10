SUBS=mcps repl tool vdb wmcp bot term

all:
	for a in $(SUBS); do $(MAKE) -C $$a || exit 1; done

fmt:
	$(MAKE) -C .. fmt

.PHONY: install uninstall

install: uninstall
	mkdir -p ~/.vim/mai
	cp mai.vim ~/.vim/mai/
	cp prompts.txt ~/.vim/mai/
	echo "source ~/.vim/mai/mai.vim" >> ~/.vimrc

uninstall:
	[ -f ~/.vimrc ] || :> ~/.vimrc
	-cp -f ~/.vimrc ~/.vimrc.bak && grep -v "source ~/.vim/mai/mai.vim" ~/.vimrc.bak > ~/.vimrc
	rm ~/.vimrc.bak
	rm -rf ~/.vim/mai

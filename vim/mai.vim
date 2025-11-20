function! Mai() range abort
  " 1) Read prompts.txt
  let l:file = expand('~/.vim/mai/prompts.txt')
  if !filereadable(l:file)
    echoerr "File not found: " . l:file
    return
  endif
  let l:lines = readfile(l:file)
  if empty(l:lines)
    echoerr "prompts.txt is empty"
    return
  endif

   " 2) Let the user select a prompt
    echo "# Select a prompt:"
    for i in range(len(l:lines))
      echo printf('%d. %s', i + 1, l:lines[i])
    endfor
    echo ".."
    echo "e. Edit prompts"
    echo "i. Inline prompt"
    echo ".."
    let l:choice = input('Enter choice (1-' . len(l:lines) . ' or e or i): ')
     if l:choice == 'e'
       execute 'edit ' . l:file
       return
     elseif l:choice == 'i'
       let l:prompt = input('Enter your custom prompt: ')
     else
       let l:choice = str2nr(l:choice)
       if l:choice < 1 || l:choice > len(l:lines)
         echo "Cancelled"
         return
       endif
       let l:prompt = l:lines[l:choice - 1]
     endif

   " 3) Get provider and model from global variables
   let l:provider = get(g:, 'mai_provider', 'openai')
   let l:model = get(g:, 'mai_model', 'gpt-4o')

   " 4) Get the selected text (or current line) to use as stdin
  let l:first = a:firstline
  let l:last  = a:lastline
  let l:stdin = join(getline(l:first, l:last), "\n")

   " 4) Run: send selected text as stdin, prompt as argument
   let l:cmd = 'mai -p ' . shellescape(l:provider) . ' -m ' . shellescape(l:model) . ' ' . shellescape(l:prompt)
  let l:out = systemlist(l:cmd, l:stdin)

  echo "\n\n----\n"
  echo join(l:out, "\n")
  echo "\n"

    " 6) Ask user what to do with the output
    let l:defaction = get(g:, 'mai_defaction', 2)
    echo '----'
    echo 'What do you want to do with the output?'
    echo '  1. Ignore'
    echo '  2. Replace selected text'
    echo '  3. Append below'
    echo '  4. C preprocessor block'
    echo '  5. Show in a separate split'
    echo '----'
    let l:ans = input('Enter choice (1-5, default ' . l:defaction . '): ')
    if empty(l:ans)
      let l:ans = l:defaction
    else
      let l:ans = str2nr(l:ans)
    endif
  if l:ans == 1
    echo "Ignored."
    return
  endif
  if empty(l:out)
    echo "No output to apply."
    return
  endif

   if l:ans == 5
   " 5) Show output in a temporary scratch buffer
   botright new
   setlocal buftype=nofile bufhidden=wipe nobuflisted noswapfile nowrap
   call setline(1, empty(l:out) ? ['(no output)'] : l:out)
   execute "file Mai Output"
   elseif l:ans == 2
     " Replace the selected range
     execute l:first . ',' . l:last . 'delete _'
    call append(l:first - 1, l:out)
    echo "Replaced."
  elseif l:ans == 3
    " Append below
    call append(l:last, l:out)
    echo "Appended."
   elseif l:ans == 4
     " C preprocessor block
     let l:old_lines = getline(l:first, l:last)
     execute l:first . ',' . l:last . 'delete _'
     call append(l:first - 1, ['#if 0'] + l:old_lines + ['#else'] + l:out + ['#endif'])
     echo "Replaced with C preprocessor conditional block."
  else
    echo "Invalid option."
  endif
endfunction

" Key mappings:
" Normal mode = current line, Visual mode = selection
" nnoremap <leader>m :call Mai()<CR>
" xnoremap <leader>m :<C-U>call Mai()<CR>
" xnoremap m :<C-U>call Mai()<CR>

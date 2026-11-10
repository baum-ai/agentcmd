# Embedded-language semantics: Python and JavaScript subsets

Both frontends run on the pure-Go tree-sitter runtime (see
`docs/compatibility.md`) and implement the same shape of analysis:

1. **Lexical bindings.** Imports bind local names to qualified library names
   (`from pathlib import Path as P` → `P` = `pathlib.Path`; `const { rmSync:
   rm } = require('node:fs')` → `rm` = `fs.rmSync`). Any later assignment,
   `def`/`function`, class or parameter with the same name rebinds it; the
   library meaning is lost and a `shadowed_binding` diagnostic is recorded.
   Wildcard imports and dynamic `require` produce no bindings.
2. **Bounded values.** String literals, adjacent/`+` concatenation, template
   or f-strings whose interpolations are literal, `os.path.join`/`path.join`,
   `Path(...) / "x"`, `path.resolve`, `os.getcwd()`/`process.cwd()` and the
   working directory after `os.chdir`/`process.chdir` are evaluated. Anything
   else is `symbolic` and shows its source text (`sys.argv[1]`,
   `${process.env.HOME}/.npmrc`).
3. **Known calls.** Only qualified library calls produce actions. A method on
   a `Path` object is recognized through the value's provenance; a method on
   an arbitrary object named `fs` is not.
4. **Control flow.** `if`/`try`-handlers/`switch` bodies are `conditional`;
   loop bodies are `repeated`; `with`/`try` bodies inherit their parent.
   Function bodies are `latent` until the function is called at module
   level, when the body is expanded once under a `function call` node with
   parameters bound to the literal arguments. JavaScript callbacks passed to
   known calls, `.then/.catch/.finally` handlers and `new Promise` executors
   are analyzed under a `callback` function node as `conditional`.
5. **Assumptions on every action.** "`X` denotes the standard library API",
   "preceding statements executed without raising", and the working-directory
   assumption inherited from the launcher. Relative paths resolve against
   the interpreter's working directory; with an unknown directory they stay
   unresolved.

Nothing is executed or evaluated by an interpreter: the folding above is a
bounded Go operation over the documented forms.

## Python calls (`languages/python.go`)

| Call | Action |
|---|---|
| `open(p, mode)`, `io.open`, `Path.open` | read for `r`; write truncate/append/create_exclusive for `w`/`a`/`x`; `+` adds the other side; non-literal mode → read and write(unknown) |
| `Path.read_text/read_bytes` | read |
| `Path.write_text/write_bytes` | write truncate |
| `Path.unlink`, `os.remove/unlink`, `Path.rmdir`, `os.rmdir/removedirs` | delete |
| `shutil.rmtree` | delete recursive |
| `Path.mkdir`, `os.mkdir/makedirs` | write directory (`exist_ok`/`makedirs` → mode unknown) |
| `Path.touch` | write unknown |
| `Path.rename/replace`, `os.rename/replace`, `shutil.move` | move |
| `shutil.copy/copy2/copyfile/copytree` | copy |
| `Path.exists/is_file/is_dir/stat`, `os.path.exists/isfile/isdir/getsize/getmtime/islink`, `os.stat/access/chmod` | metadata |
| `Path.iterdir`, `os.listdir/scandir/walk` | list |
| `Path.glob/rglob`, `glob.glob/iglob` | search.path (matches not enumerated; loop variables over the result are path-typed matches) |
| `subprocess.run/call/check_call/check_output/Popen` | list argv → nested command with `cwd=`; string with `shell=True` → nested shell analysis; string without shell → execution of that program; non-literal → `process.unresolved` |
| `os.system`, `os.popen`, `subprocess.getoutput/getstatusoutput` | nested shell analysis |
| `os.exec*` | execution of the program |
| `os.chdir` | changes the abstract working directory |

## JavaScript calls (`languages/javascript.go`)

Modules: `fs`, `node:fs`, `fs/promises` (normalized to `fs.*`), `fs.promises.*`,
`child_process`, `path`, `process`; `fs-extra`/`graceful-fs` are treated as
`fs`. Both the callback and `Sync` forms map to the same action.

| Call | Action |
|---|---|
| `fs.readFile[Sync]`, `createReadStream` | read |
| `fs.writeFile[Sync]`, `createWriteStream`, `outputFile` | write truncate (`{flag:'a'}` → append) |
| `fs.appendFile[Sync]` | write append |
| `fs.open[Sync](p, flags)` | by flags as `open()` modes |
| `fs.unlink[Sync]`, `rmdir[Sync]`, `remove` | delete |
| `fs.rm[Sync]` | delete with `recursive`/`force` from options |
| `fs.mkdir[Sync]`, `ensureDir`, `mkdtemp` | write directory |
| `fs.readdir[Sync]`, `opendir` | list |
| `fs.stat/lstat/access/exists/chmod/chown/utimes/realpath/watch` | metadata |
| `fs.copyFile[Sync]`, `cp`, `copy` | copy |
| `fs.rename[Sync]`, `move` | move |
| `fs.symlink/link` | write create_exclusive |
| `fs.truncate` | write truncate |
| `child_process.exec[Sync](cmd)` | nested shell analysis (`cwd` option honoured) |
| `child_process.execFile/spawn[Sync](file, args, opts)` | nested command; `shell: true` joins and analyzes as shell |
| `child_process.fork(module)` | `process.interpreter` with the module as a file resource |
| `process.chdir` | changes the abstract working directory |

## Known gaps

Classes and methods on user objects, `getattr`/computed member access,
decorators that wrap functions, generators, `async` scheduling order,
`import()` of non-builtin modules, TypeScript-only syntax, and any library
outside the tables above yield symbolic values or no actions. A successful
parse never implies a closed-world effect inventory: imported modules can
have behaviour outside the snippet.

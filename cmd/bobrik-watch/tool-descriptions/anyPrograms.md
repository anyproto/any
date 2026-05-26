Program management — create, update, list, and inspect JS programs stored in the space.

## Tool Schema

### listPrograms()
List all programs in the space. Returns [{id, name, version, title}].

### getProgram(name, version?)
Get a program's source code. Returns {id, name, version, source}.
- name: program name
- version: version string (default "v1")

### createProgram(opts)
Create a new program.
- opts.name: program name (required)
- opts.source: JS source code (required)
- opts.version: version (default "v1")

### updateProgram(opts)
Update an existing program's source.
- opts.name: program name (required)
- opts.source: new source code (required)
- opts.version: version (default "v1")

### runProgram(name, args, version?)
Execute a program.
- name: program name
- args: arguments object
- version: version string (default "v1")

### editProgram(programName, opts)
Surgical string replacement on program source.
- programName: program name
- opts.oldString, opts.newString, opts.replaceAll

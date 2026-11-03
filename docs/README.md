# Documentation

This folder holds the documentation of govoip-dsp: guides for people who
use the library, and records for people who change it. You need no audio
background for the user guides; the glossary explains each term.

## For users

Read these in this order. Start with the first two if you write an Asterisk
AudioSocket service, and with the third otherwise.

1. [AudioSocket quick start](audiosocket.md): for a Go service that takes
   calls from Asterisk over AudioSocket. It explains a complete program
   and how to run and change it.
2. [examples/audiosocket](../examples/audiosocket/main.go): the program
   that the AudioSocket quick start explains. Run it from the repository
   root with `go run ./examples/audiosocket`.
3. [Developer guide](guide.md): the main path for every other
   application. It explains the audio basics, which packages you need,
   and how to call them for each frame.
4. [Glossary](glossary.md): look up a term such as frame, near end or
   echo delay when you meet it.
5. [usage.md](usage.md): the full recipes for wiring the packages into
   one call across goroutines, with their caveats and measurements. Read
   it when you build a real call.
6. [API reference on pkg.go.dev](https://pkg.go.dev/github.com/danieldonoghue/govoip-dsp):
   every exported type and function, with examples. Keep it open while
   you write code.

## For contributors

These are records for people who change the library. You do not need
them to use it.

- [requirements.md](requirements.md): the specification. It wins over
  every other plan.
- [contracts.md](contracts.md): the conventions and interfaces that every
  package follows.
- [architecture.md](architecture.md): the packages, how they fit
  together, and the work packages that built them.
- [acceptance/](acceptance/): the acceptance criteria of each component,
  written before its code.
- [testing.md](testing.md): how the library is tested, where the
  reference data comes from, and the results.
- [algorithms.md](algorithms.md): each component's algorithm, reference
  behaviour, parameters and known deviations.
- [performance.md](performance.md): measured results, each with its
  environment and command.
- [decisions/](decisions/README.md): the architecture decision records
  (ADRs), with an index.

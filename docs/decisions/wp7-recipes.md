# ADR wp7-recipes: Two wiring recipes, with `Cancel` for paced sending

- **Status:** accepted.
- **Date:** 2026-10-27

## Context

`docs/usage.md` shows how to wire one call's send path. The far end that
the echo canceller needs can reach the application in two ways:

- the application chooses what to send for each received frame, as on
  Asterisk AudioSocket or external media, where Asterisk forwards the
  call's frames on the call's own clock and writes the application's
  audio as it arrives, without re-timing it or adding silence
  (`apps/app_audiosocket.c`, `channels/chan_audiosocket.c`);
- an audio API delivers playback and capture on separate callbacks, each
  with its own timing.

With `Playback` before each `Capture`, the canceller sees the far end one
frame late (`mdf.c:517`, `mdf.c:656`). The Speex manual advises
`speex_echo_cancellation` when playback and capture are in step.

## Decision

- `docs/usage.md` has two recipes. Each opens with the use cases it fits
  and does not fit, then its caveats, then the code, which is copied
  verbatim from tests in `integration/`.
- **Recipe A, paced sending.** The application writes what it wants to
  send into a pending SPSC. For each received frame, the processing
  goroutine takes at most one pending frame, or silence, hands it to the
  network writer and calls `Cancel` with the received frame and that same
  frame. The in-step source (one sound card driving both streams) is a
  note within recipe A.
- **Recipe B, separate callbacks.** The output callback's played frames
  and the input callback's captured frames reach one processing goroutine
  through SPSCs. For each captured frame it calls `Playback` with exactly
  one played frame, or silence, then `Capture`.
- Criterion U6 (`docs/acceptance/integration.md`), a 32 ms echo with 0 to
  30 ms of jitter, stays exactly as registered, as a pinned finding: a
  delay the recipes do not cancel.
- Criterion U11 covers 100 and 200 ms echoes with the same jitter and a
  256 ms filter, with the U6 threshold, plus a reported recovery after a
  delay step from 100 to 140 ms. The threshold was set before any figure
  with a longer delay existed; only the 100 ms case had been measured
  (`docs/acceptance/integration.md`, "Changes").

## Consequences

- Recipe A cancels without a lag. Measured against the `Playback` and
  `Capture` path on the same paced call, it passed the 32 ms echo without
  jitter that the other failed, and was worse only by 0.1 to 1.2 dB in
  criteria U11 at 8 kHz and U12 (`docs/acceptance/integration.md`,
  "Changes").
- Recipe B keeps a one-frame lag. It needs an echo delay above about two
  frame periods without jitter, and of 60 ms or more with up to 30 ms of
  jitter (`docs/usage.md`, recipe B, "Caveats").
- Both recipes pass U11 at 8 and 16 kHz (`docs/testing.md`, "Whole-chain
  measurements").
- Neither recipe corrects clock drift between the two streams (S2), and
  recipe A has no backlog policy after a processing stall
  (`docs/architecture.md`, "Future work").

## Alternatives considered

- **Pairing the frames on the network goroutine.** Not built.
- **One in-step recipe only.** It does not fit an application that sends
  only when it has audio.
- **`Playback` and `Capture` for paced sending.** Measured, and replaced
  by `Cancel` for the reasons above.
- **A de-jitter buffer for the sent frames.** Not built; one frame per
  received frame gives the same pairing without a fixed delay.

# The design

This is the design of go-matter's API, agreed before it was built. The API itself is in
the packages, as Go, built in part; this document holds what the code cannot say: what
the API holds itself to, the decisions in it and why, how it lands on Matter, and what is
left out. How it is wired to an implementation of Matter is not here.

## What it is for

A home has devices that speak their makers' protocols. Two things are wanted of them:

- to **serve** them to Matter controllers, as one bridge with the devices behind it;
- to **control** Matter devices, that bridge among them, from a program of our own.

go-matter is the API for both, over an implementation of Matter that it hides entirely.

## Where it is

The API is the code. Each package's documentation is its design, and its examples are
compiled with it:

| Package | What it is |
| --- | --- |
| [`matter`](../matter.go) | the vocabulary: devices, their state and commands; [sources and events](../source.go); [the home](../home.go) that devices are written into; [the errors](../errors.go) |
| [`bridge`](../bridge/bridge.go) | serves a source of devices as one Matter bridge |
| [`controller`](../controller/controller.go) | commissions nodes and gives each back as a source |
| [`mattertest`](../mattertest/mattertest.go) | devices that do as asked, and the protocol whole in one test process |

Examples: [an adapter writing a maker's dimmers into a home](../example_test.go),
[a bridge daemon](../bridge/example_test.go) and
[a controller](../controller/example_test.go).

All of it is built: package `matter`, the bridge, the controller, and `mattertest`, whose
`Across` runs the two against each other in one process.

## What the API holds itself to

1. **One vocabulary, both sides.** What a device is and does is said once, in package
   `matter`. A maker's adapter writes it, the bridge serves it, the controller gives it
   back. A program written against it cannot tell a device behind a maker's hub from one
   that crossed the protocol.
2. **State is data, commands are functions, change is events.** A `Device` is a value
   that says how the device stood when it was read. Nothing is asked of the network to
   read it, and nothing changes it behind the reader's back. There is one way to learn
   each thing, not two.
3. **The hard part is written once.** Telling several watchers everything, in order,
   with nothing lost between the first look and what follows, is `Home`'s. An adapter
   writes devices into a home and reports what its hub says; it implements no interface
   and keeps no promise about concurrency.
4. **A command returns an error, and the error crosses.** What a device refuses behind
   the bridge is what the controller's caller is refused, as the same error value.
5. **Open, or not at all.** `Open` returns something that works, or an error. There is
   nothing to attach afterwards, no state in which a call must wait or be ordered by
   hand, and `Close` returns when everything that was started has ended. Nothing that
   happens on the network ends a bridge or a controller: a device that is gone is
   unreachable, and is looked for again.
6. **The zero value is the safe one.** A zero `Device` is reachable, a zero `Trust` is
   the strictest, a zero `Home` is empty and ready, a nil command is one the device does
   not have.
7. **A word exists when a device needs it.** Five kinds of device and three
   capabilities, because the devices served so far are switches, dimmers, plugs, button
   remotes, fans and air purifiers. Matter has many more device types; each is added the
   day one is served.
8. **No numbers, and nothing of the implementation.** No endpoint, cluster, attribute or
   command identifier appears; a level is a fraction and a time is a `time.Duration`. No
   exported type, error or option belongs to the library underneath, and there is no way
   to reach around the vocabulary to it. That is a cost accepted on purpose: every door
   through is a design decision imported.

## The decisions, and why

- **A capability is a struct of state and functions, not an interface.** `lamp.OnOff.On`
  is how it stands and `lamp.OnOff.Set` switches it. An optional command is a nil
  function, where interfaces would need one per combination; an adapter writes closures
  over its maker's library, where interfaces would need a type per device.
- **`Watch` begins with every device as it stands.** A watcher that starts late has
  missed nothing, and there is no recipe of watching first and reading after to get
  right. It is also what a Matter subscription does.
- **Off is not a level.** A dimmer that is off keeps the level it returns to, as Matter
  has it and as a wall dimmer behaves. `LevelControl.Set` brings the device on, which is
  what dimmers do; `OnOff.Set(true)` returns it to where it was. A home keeps that level
  for an adapter whose hub does not.
- **A fan is off, on or left to itself, and has a speed beside.** Which of low, medium
  and high a speed is would be someone's guess, so the vocabulary has no such words: a
  speed is a fraction of the fastest, and a fan that has steps says how many. A fan that
  is off, or that chooses its own speed, keeps the one it comes back to, as a dimmer
  keeps its level. What a fan does is told whole, in one change: its mode, its speed and
  whether it turns move together, and a watcher told them apart would see states the fan
  was never in.
- **A command returns when it is taken, not when it is done.** What the device then does
  arrives as an event like any other, so state is never guessed at.
- **An unreachable device is answered for by its source.** Its commands return
  `ErrUnreachable` without the device being tried. That is one rule kept in one place,
  the home, and not a duty of every adapter's every command.
- **A watch ends in one of three ways.** With its context; with `ErrClosed` when the
  source is over for good, a controller closed or a node unpaired; or with another error
  when it failed and may be watched again. The bridge tells the last two apart.
- **A remote says down and up, and no more.** What a long press or a double press is, is
  for whoever hears them to decide. The bridge derives Matter's long and multiple presses
  for the controllers that want them; the vocabulary does not carry a second opinion.
- **One `Invite` on each side.** The first administrator and every later one are let in
  the same way, for a stated time. Nothing is commissionable by default or forever.
- **A name is the maker's.** A controller cannot rename a device through the bridge; it
  names it in its own home, as home apps do.
- **State is kept in a directory.** It is the least that can be asked and hides the most.
  A test gives a temporary one.
- **Logging is `log/slog`'s default.** The host says where it goes, once, for everything.
- **`mattertest.Across` is the module's law.** A source, served by a bridge and
  commissioned by a controller, is the same source. Every test of this module is a case
  of it, and so is the test of any adapter. It is also what a second implementation of
  Matter would have to pass to take the first one's place.

## How it lands on Matter

The API never says these; the implementation has to. The device types, clusters and
status codes are named from the specification as known, and were not checked against its
text for this document.

| In the vocabulary | In Matter |
| --- | --- |
| a bridge | a node with an Aggregator, each device a Bridged Node under it |
| `Light` with `OnOff` | On/Off Light |
| `Light` with `LevelControl` | Dimmable Light |
| `Plug` with `OnOff` | On/Off Plug-in Unit |
| `Plug` with `LevelControl` | Dimmable Plug-in Unit |
| `Remote` | one Generic Switch per button, labelled |
| `Fan`, `AirPurifier` | Fan, Air Purifier |
| `OnOff` | the On/Off cluster: On, Off and Toggle all arrive as `Set` |
| `LevelControl.Set` | Level Control's MoveToLevel, with the device on or coming on |
| `LevelControl.Move`, `Stop` | Level Control's Move and Stop |
| `FanControl.Mode` | Fan Control's FanMode, of the sequence off, high and auto: on is High, and Low and Medium written by a controller are on |
| `FanControl.Speed` | PercentSetting, and SpeedSetting of SpeedMax for a fan with `Steps`, which is what is written and read when there are steps |
| `FanControl.Running` | PercentCurrent and SpeedCurrent, zero for a fan that does not turn |
| `FanControl.Oscillating`, `Sleep` | RockSetting's left and right, WindSetting's sleep wind |
| `Level`, above 0 to 1 | CurrentLevel, 1 to 254 |
| a `time.Duration` | TransitionTime, in tenths of a second |
| `ButtonPressed`, `ButtonReleased` | the Switch cluster's InitialPress and ShortRelease; LongPress and MultiPress derived by the bridge |
| `Unreachable` | Bridged Device Basic Information's Reachable |
| `Info` | Bridged Device Basic Information's labels; `Room` as a fixed label |
| `Invite` | opening a commissioning window |

A level asked of a device that is off, without switching it on, is something Matter has
and the vocabulary does not: the bridge keeps that level and uses it at the next
`OnOff.Set(true)`.

Matter says no speed of a fan that is off, and none of one left to itself: PercentSetting
is zero for the one and null for the other. A controller keeps the speed the fan last
said, and takes it for full when it said none. A controller's write of what a fan
already is at reaches no one: Matter's fan compares before it asks.

| Error | Status sent, and read back as the same error |
| --- | --- |
| `ErrInvalid` | CONSTRAINT_ERROR |
| `ErrBusy` | BUSY |
| `ErrUnreachable` | TIMEOUT |
| `ErrFailed`, and any other | FAILURE |

## Left out, on purpose

- Colour, sensors, locks, covers, thermostats and every other kind: principle 7.
- Scenes and groups. A controller that wants several lights at once calls several.
- Reading or writing a cluster by number: principle 8.
- Commissioning over Bluetooth, onto Wi-Fi or Thread. Everything here is already on the
  network.
- Listing what is commissionable nearby. `Pair` takes a code.
- Firmware updates of other makers' devices.
- Joining sources. Adapters write into one home, each naming its devices so that no two
  collide.

## What the first implementation does not do

The bridge and the controller are built on
[go-fabric](https://github.com/SukramJ/go-fabric), behind
[`internal/fabric`](../internal/fabric/serve.go), the one package that names it. Where it
falls short of the API, the API stays as it is and the shortfall is here:

- **A device's model, serial and firmware do not cross.** Its name, vendor and room do.
  The library fills a bridged device's product name and serial number from one value of
  its own, and offers no place for a software version.
- **`Interface` restricts where the bridge announces itself, not where it answers.**
- **`Move` and `Stop` are answered as unsupported for a device without them.** The
  bridge does not step such a device by `Set`. Matter's Step commands, and on for a
  time, are answered as unsupported for every device.
- **A level sent to a device that is off is always kept**, for when it is switched on.
  Matter keeps it only when the command or the device says to; the bridge does not look.
- **A name is cut at 32 bytes and a room at 16**, which is what Matter gives each.
- **A node opens to commissioning by itself** when no fabric holds it. The bridge closes
  what `Invite` did not open, a moment after it opens.
- **A remote crosses as one remote for each of its buttons**, each named by the remote
  and the button. Matter has a switch for a button and nothing here groups them again.
- **Every dimmable device has `Move` and `Stop` on the far side.** Matter requires the
  commands of every such device; one that has none behind the bridge answers them with
  `ErrFailed`.
- **A device with no vendor of its own is its bridge's**, on the far side.
- **The controller's `Interface` is not honoured.** It uses every interface.
- **The controller does not run on Windows.** To find devices the library first asks the
  system to say which interface each packet came in on, which `golang.org/x/net` does not
  implement there, and it gives up. `Open` is `errors.ErrUnsupported`.
- **Whether the bridge can be found on Windows is not verified.** It opens there. Its
  announcement is made through the same call, whose failure the library logs and goes
  on from; a bridge that announces nothing is one no controller finds.

## To settle when it is built

These do not change the API; they are where the implementation has to check the
specification, or a real controller.

1. **`ErrUnreachable` as TIMEOUT.** It keeps the error distinct across the protocol;
   whether other controllers read TIMEOUT kindly is not known.
2. **Identity through someone else's bridge.** A device behind a bridge that is not ours
   has a stable ID only if that bridge gives it a unique one.
3. **How long an invitation can last.** Matter bounds a commissioning window; `Invite`
   refuses, with the library's own reason, one it cannot keep.
4. **Every name in the tables above.**
5. **That a node which went away is found again.** It is the library's to do, and no test
   here shows it: on a test's network a node stopped and started again was still
   unreachable a minute later, for a reason not found.

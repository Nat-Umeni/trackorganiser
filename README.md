# TrackOrganiser

Pulls your Spotify playlists and saves each track as a tagged MP3, one folder per
playlist, ready for Mixxx or VirtualDJ.

Spotify supplies the metadata — track name, artists, album, cover art, and the
date you added it. YouTube supplies the audio.

- **320kbps MP3**, named `Song - Artist.mp3`
- **Proper ID3 tags** written from Spotify, not from YouTube video titles
- **Album art embedded**, so covers show in your DJ software
- **Your playlist order preserved** as the file's modified date, so sorting a
  folder by date gives you back Spotify's newest-first view
- **Re-runs skip what you already have**, and fix the tags on what you do

## Getting started

### 1. Download it

Grab the file for your system from the [releases page](../../releases). You don't
need Go, or anything else installed to run it.

| your system | file |
|---|---|
| Windows | `trackorganiser-windows-amd64.exe` |
| Windows on ARM | `trackorganiser-windows-arm64.exe` |
| Mac (M1 and later) | `trackorganiser-macos-arm64` |
| Linux | `trackorganiser-linux-amd64` |

**Make sure you grabbed the right one.** They are not interchangeable - the
Windows `.exe` will not run on Linux or Mac, and the error is an unhelpful "no
such file or directory". If in doubt:

```
file trackorganiser-*
```

`ELF` means Linux, `Mach-O` means Mac, `PE32+` means Windows.

**Then rename it**, dropping the platform but keeping any `.exe`:

| | rename to |
|---|---|
| Windows | `trackorganiser.exe` |
| Mac and Linux | `trackorganiser` (no extension) |

Every command below assumes you did. On Mac and Linux you also need to allow it
to run:

```
chmod +x trackorganiser
```

**On Windows, Defender will probably block it.** You'll get "Windows protected
your PC", because the file isn't code-signed and signing costs money. Click
**More info**, then **Run anyway**. If you'd rather not take my word for it,
build it yourself - the source is this repo.

### 2. Run it once with your client ID

**Open a terminal in the folder you downloaded it to.** Don't double-click the
file: it does nothing useful without the step below, and the window will close
before you can read why.

On Windows, Shift+right-click in the folder gives you "Open PowerShell window
here", or type `cmd` into the address bar.

Whoever sent you this program has a Spotify client ID. Ask them for it, then:

**Windows** (PowerShell needs the `.\`):

```
.\trackorganiser.exe -client-id THE-ID-THEY-GAVE-YOU
```

**Mac and Linux** (note the forward slash):

```
./trackorganiser -client-id THE-ID-THEY-GAVE-YOU
```

That only needs doing once. It's saved in your user config directory and picked
up automatically from then on.

A browser tab opens so you can log in to Spotify and approve access. If it
doesn't open, the program prints a link to paste instead.

### 3. Say yes to the downloads

On first run it offers to fetch the two tools it needs. Both go in your user
cache directory, not onto your system, so nothing is installed behind your back
and deleting that folder undoes it.

| | |
|---|---|
| yt-dlp | small, fetches the audio |
| ffmpeg | 150-190 MB, around 335 MB unpacked, converts it to MP3 |

ffmpeg is big because these are self-contained builds. If you'd rather use your
own, install it first and the program will prefer that:

| | |
|---|---|
| Windows | `winget install ffmpeg` |
| Mac | `brew install ffmpeg` (**required** - no automatic download exists for Mac) |
| Arch / CachyOS | `sudo pacman -S ffmpeg` |
| Debian / Ubuntu | `sudo apt install ffmpeg` |

## Using it

From the same terminal, run it with no arguments. You'll be asked two things.

**Which playlists.** They're listed with numbers, and you can use commas, ranges,
or both:

```
1,3,5      playlists 1, 3 and 5
4-7        playlists 4 through 7
1-20,25    playlists 1 through 20, plus 25
```

**Where to save them.** Press enter for the folder you used last time (`~/Downloads` the first time), or type somewhere else - it remembers whatever you pick.
Each playlist becomes its own folder inside it, so choosing `~/Music` gives you
`~/Music/Mix Potential`, `~/Music/Soul Food`, and so on.

### Going faster

Downloads run four at a time by default. If your connection can take it, turn it
up:

```
.\trackorganiser.exe -jobs 16      # Windows
./trackorganiser -jobs 16          # Mac and Linux
```

16 is the maximum. On a reasonable machine that's roughly seven times quicker
than one at a time — a 60-track playlist drops from about 7 minutes to about 1.

## Updating

It checks for a new version each time it starts and asks before installing it:

```
Version v0.3.0 is out (you have v0.2.0). Update now? [Y/n]
```

Say yes and it downloads the new version, checks it against the release's
checksum, and replaces itself. It keeps the name you renamed it to. Then **run it
again** to use the new version.

Say no and it won't ask about that version again, only the one after it.

If you have **v0.1.0**, it can't update itself because it predates this feature.
Download the latest version from the [releases page](../../releases) once by
hand, and it takes care of itself from then on. `-version` tells you which you
have.

## Options

```
-jobs 4      how many tracks to download at once (1-16)
-dry-run     list what would be downloaded, download nothing
-no-retag    skip rewriting tags on tracks you already have (much faster)
-client-id   your Spotify client ID, only needed once
-version     print the version
```

## Things worth knowing

**It only sees playlists you own or collaborate on.** Spotify returns an error for
anything you merely follow. Make your own copy of a playlist to download it.

**Some tracks will be wrong or missing.** The audio comes from a YouTube search,
so occasionally it finds a different version, or nothing at all. It checks that
what it found has the right song name, one of the right artists and Spotify's
length, and when it doesn't, it looks further down the search results or tries a
different search before giving up. Anything still suspicious is listed at the
end of the run for you to check by ear. A song it can't find at all isn't saved -
you won't get a different song in its place - and it tries again next run.

**Some come from SoundCloud.** When YouTube doesn't have a track - often an
artist's own remix or bootleg - it checks SoundCloud with the same care. Some
SoundCloud tracks are copy-protected and can't be downloaded; the end of the run
tells you which, and the filename to use. Buy the track (Bandcamp and Beatport
usually sell a better-quality file than any stream), save it as an mp3 with that
name in the playlist folder, and the next run tags it like the rest.

**Nothing is ever deleted.** Removing a track from a playlist leaves the file
alone, and deleting a file means it gets downloaded again next run.

**Re-running is cheap.** Tracks already on disk are skipped, and the ones you have
get their tags, cover art and dates brought back in line. Tracks you've filed
into subfolders by hand still count as downloaded.

# eggsfm

sync'd realtime audio thing.

NOTE: no actual radio logic yet. see the tracking issue [here](https://github.com/philipch07/EggsFM/issues/18).

nerd stuff (streaming)
- [x] webrtc audio only
- [ ] hls fallback + hls m3u support
    - [ ] efficient (cached) transcoding of audio files

support goals
- [x] chrome
- [x] edge (this wasn't on purpose)
- [ ] ffox
- [ ] apple stuff
- [ ] android stuff
- [ ] car
- [ ] fridge?
- [ ] let me know if u can tune in on a ti84

# how to listen

coming soon!

# how to host my own

in `/media/` you can put in any media that you own or that is in the public domain, so long as it's `.opus`.

right now it will loop through the `.opus` files in the `/media/` folder.

please note that in the future this will shift to focus more on playlists (aka once the radio logic is implemented, but i'll leave a simple loop mode since it's useful still)

# for devs
you'll need 2 terminals:
### terminal 1 (at the root):
```
go run .
```
this launches the backend on :8080

### terminal 2 (make sure you've `cd`'d into `/web/`)
```
npm install
npm run dev
```

this launches the frontend on `localhost:5173`.

# This project is based on broadcast-box and has been heavily modified.

please check out the original project. it's really cool. https://github.com/Glimesh/broadcast-box

the original project is really easy to use to stream with friends (you only need obs!): https://github.com/Glimesh/broadcast-box?tab=readme-ov-file#using
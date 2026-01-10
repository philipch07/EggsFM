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
- [x] ffox
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

## systemd deployment

The repo includes a systemd service at `packaging/systemd/eggsfm.service` and an installer at `scripts/install-systemd-service.sh` that builds and runs EggsFM as a service.

1. run `sudo scripts/install-systemd-service.sh` to install EggFM as a service.
2. Edit `INSTALL_DIR/.env.production` for your host.
3. to make sure it's installed, run `sudo systemctl status eggsfm`.
4. the install dir is group-writable. so that you can add your deploy user to the `eggsfm` group (`sudo usermod -aG eggsfm <user>`), then run `./scripts/rebuild-eggsfm.sh`. so you can rebuild without sudo.

# This project is based on broadcast-box and has been heavily modified.

please check out the original project. it's really cool. https://github.com/Glimesh/broadcast-box

the original project is really easy to use to stream with friends (you only need obs!): https://github.com/Glimesh/broadcast-box?tab=readme-ov-file#using
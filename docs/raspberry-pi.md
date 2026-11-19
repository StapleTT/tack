# Run tack on a fresh Raspberry Pi

Use a Raspberry Pi 5 with **Raspberry Pi OS Lite (64-bit), Debian Trixie**, Ethernet, and SSH. The Pi relays encoded video and audio. Sunshine runs on the gaming PC, and the laptop only needs a browser. A desktop environment, GPU devices in Docker, and a monitor on the Pi are unnecessary.

**Choose one installation.** Docker Compose is the shortest path to a boot-persistent bridge. The native installation uses a systemd service and puts its binary in `/usr/local/bin`. Both serve the same panel and use the same state-file format. Run only one bridge on the default ports.

## 1. Prepare the Pi and gaming PC

1. In Raspberry Pi Imager, select the 64-bit Lite image. Set a hostname, create your login, and enable SSH. Boot the Pi, then SSH into it with that login.
2. Give the Pi and gaming PC DHCP reservations on your router. Use the **Pi’s LAN IPv4 address** for the browser and agent. The bridge generates its HTTPS certificate for the addresses present at startup, including localhost.
3. Install and start Sunshine on the gaming PC. Open its local dashboard at `https://localhost:47990`, finish Sunshine setup, and confirm an application named **Desktop** is available. If using a host firewall, allow Sunshine’s GameStream traffic from the Pi. Test while the gaming PC is awake and the desktop session is running.
4. Allow laptops and the gaming PC to reach the Pi over the LAN. Guest Wi-Fi with client isolation can prevent this.

| Destination | Incoming port | Used for |
|---|---|---|
| Pi | TCP 8443 | HTTPS panel, session setup and host-agent WebSocket |
| Pi | UDP 8189 | Browser WebRTC media and input |
| Gaming PC | Sunshine’s GameStream ports | Bridge-to-host pairing, stream control and media |

No router port forwarding is needed for this LAN setup.

On the **Pi**, check the OS and clone the repository, including the nested Moonlight dependencies:

```sh
dpkg --print-architecture        # expect arm64
. /etc/os-release && echo "$VERSION_CODENAME"  # expect trixie
hostname -I                     # note the LAN address
sudo apt update
sudo apt install -y git ca-certificates curl
git clone --recurse-submodules https://github.com/StapleTT/tack.git ~/tack
cd ~/tack
```

## 2A. Docker Compose

If `sudo docker compose version` already works, skip the engine installation. Otherwise install Docker Engine and the Compose plugin from Docker’s Debian repository. These commands target the 64-bit Trixie image above.

```sh
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=arm64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" | sudo tee /etc/apt/sources.list.d/docker.list
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
sudo docker compose version
```

Build **on the Pi** so Docker selects ARM64 base images and compiles Moonlight for the Pi. Run these commands from the checkout as your regular login. Compose reads its UID/GID from `.env`, so the bridge can write the bind-mounted state as that user. On a fresh checkout:

```sh
cd ~/tack
printf 'TACK_UID=%s\nTACK_GID=%s\n' "$(id -u)" "$(id -g)" > .env
install -d -m 700 data
sudo docker compose build bridge
sudo -v
systemd-ask-password "Initial tack admin password" | sudo docker compose run --rm -T --no-deps bridge passwd
sudo docker compose up -d
sudo docker compose ps
sudo docker compose logs --tail=40 bridge
curl --fail --cacert data/tls/ca.pem https://localhost:8443/login -o /dev/null
```

Run `passwd` only for initialization or an intentional reset. It reads the password from standard input. The service starts automatically after reboot. The Linux host network lets WebRTC advertise the Pi’s LAN address and lets the local certificate cover it. The Compose file therefore has no port mappings. State survives container replacement in `~/tack/data`.

Continue with **section 3**.

## 2B. Native systemd service

Install the build dependencies on the Pi. Trixie’s packaged Go can download the newer toolchain requested by `go.mod`. The first build needs internet access for that toolchain, Go modules and npm packages.

```sh
cd ~/tack
sudo apt install -y build-essential cmake libssl-dev golang-go nodejs npm
GOTOOLCHAIN=auto go version     # in the checkout, expect Go 1.27.3 or newer
GOTOOLCHAIN=auto make bridge
sudo install -m 755 build/tack-bridge /usr/local/bin/tack-bridge
sudo adduser --system --group --home /var/lib/tack --no-create-home tack
sudo install -d -m 700 -o tack -g tack /var/lib/tack
sudo -v
systemd-ask-password "Initial tack admin password" | sudo -u tack /usr/local/bin/tack-bridge passwd -data /var/lib/tack
sudo install -m 644 deploy/tack-bridge.service /etc/systemd/system/tack-bridge.service
sudo systemctl daemon-reload
sudo systemctl enable --now tack-bridge
sudo systemctl status tack-bridge --no-pager
sudo curl --fail --cacert /var/lib/tack/tls/ca.pem https://localhost:8443/login -o /dev/null
```

This is a **system** service. It starts at boot, runs as the dedicated `tack` user and keeps state in `/var/lib/tack`. Logs are available with `sudo journalctl -u tack-bridge -b`.

## 3. Trust the bridge, pair Sunshine, and start streaming

1. Copy the **public CA certificate** to each laptop over an authenticated channel. For Docker, run `scp LOGIN@PI_IP:tack/data/tls/ca.pem tack-ca.pem` on the laptop, replacing `LOGIN` and `PI_IP`. For native, first run `sudo install -m 644 /var/lib/tack/tls/ca.pem "$HOME/tack-ca.pem"` on the Pi, then copy `LOGIN@PI_IP:tack-ca.pem`. The panel also serves the public certificate at `/ca.pem`. Keep `tls/ca.key` private.
2. Import the certificate as a trusted authority for websites. In Firefox, use **Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import**. In ChromeOS, search Settings for **Manage certificates** and import under **Authorities**. On Windows with Chrome or Edge, import into the current user’s **Trusted Root Certification Authorities**.
3. Open `https://PI_IP:8443/` on the laptop. Sign in with the password set on the Pi. In **Host**, enter the **gaming PC’s LAN IP** and choose **Start pairing**. Enter the displayed four-digit PIN on the **gaming PC** at `https://localhost:47990/pin`. Wait for the panel to report that the host is paired.
4. Start streaming. Check that the picture moves, click to enable sound if prompted, and move the pointer. **Ctrl+Alt+Shift+S** shows receiver statistics. **Ctrl+Alt+Shift+H** opens the stream menu. Starting a stream from another laptop takes it over.

## 4. Optional Hyprland host agent

The agent adds clipboard sync, host cursor shapes, and automatic switching to relative mouse mode for game windows. Install it **on the gaming PC** inside the Hyprland user’s session. The Pi service and bridge container run independently of it.

On the gaming PC, install Go and `wl-clipboard` (on Arch, `sudo pacman -S --needed go wl-clipboard git`), clone the repository and run:

```sh
cd ~/tack
make agent
install -Dm755 build/tack-agent ~/.local/bin/tack-agent
install -d -m 700 ~/.config/tack
install -Dm644 deploy/tack-agent.service ~/.config/systemd/user/tack-agent.service
```

Copy the Pi’s `agent-token` and public `tls/ca.pem` to `~/.config/tack/agent-token` and `~/.config/tack/ca.pem` on the gaming PC. For Docker, copy them over SSH from `~/tack/data/` on the Pi. For native, the files are in `/var/lib/tack/`; use an administrator account on the Pi to transfer them securely. Set the destination token mode to `600`. Replace `BRIDGE-IP` in the installed agent unit with the Pi’s LAN IPv4 address. Then, on the gaming PC:

```sh
systemctl --user daemon-reload
systemctl --user enable --now tack-agent
journalctl --user -u tack-agent -n 30 --no-pager
```

## Updates, restarts and password recovery

Restarting the bridge disconnects the active stream. After updating, refresh the browser page to load the newly embedded client.

| Task | Docker (from `~/tack`) | Native |
|---|---|---|
| Restart current build | `sudo docker compose restart bridge` | `sudo systemctl restart tack-bridge` |
| Follow logs | `sudo docker compose logs -f bridge` | `sudo journalctl -fu tack-bridge` |
| Stop | `sudo docker compose stop bridge` | `sudo systemctl stop tack-bridge` |
| Start | `sudo docker compose up -d` | `sudo systemctl start tack-bridge` |

To update, run `git pull --ff-only` and `git submodule update --init --recursive` in the checkout. For Docker, run `sudo docker compose up -d --build`. For native, run `make bridge`, stop the service, install the new binary as in section 2B, and start it. If the unit file changed, reinstall it and run `sudo systemctl daemon-reload` before starting.

Change the password in **Host → Admin password**. If locked out, stop the bridge, repeat only the `systemd-ask-password … | … passwd` pipeline for your installation, then start it again.

For a backup or migration, stop the bridge and copy the **whole state directory** with permissions preserved. It holds the password hash, paired Sunshine identity, agent token and CA private key. Restore it before starting the replacement, with ownership matching the Compose UID/GID or native `tack` account. Retaining this directory preserves pairing and laptop CA trust.

## Troubleshooting

| Symptom | Check |
|---|---|
| Build fails looking for ENet or a Moonlight source file | From the checkout, run `git submodule update --init --recursive` and rebuild. |
| Service restarts immediately | Read its logs. A missing admin password means initialization used a different state directory or was skipped. Under Docker, compare `ls -ldn data` with the UID/GID in `.env`. |
| “Address already in use” | Inspect `sudo ss -ltnup` for TCP 8443 and UDP 8189. Check for another bridge before starting either installation. |
| Certificate warning after import | Use the Pi’s LAN IPv4 address. After changing the Pi’s address, restart the bridge to reissue its server certificate. Verify the CA came from this bridge’s state directory. |
| Panel works but the stream fails | Verify the panel shows the gaming PC online and paired, Sunshine is capturing the desktop, and UDP 8189 can reach the Pi from the laptop. Check that Docker uses host networking. |
| Clipboard or automatic game mode is missing | Check the agent log on the gaming PC, its bridge IP, token and CA, and whether the user service can access the running Hyprland session. |

The project roadmap still lists physical Pi and laptop validation as outstanding. A successful login checks HTTPS; playing video with sound and input checks the media path.

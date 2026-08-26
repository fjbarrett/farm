# Device inventory

Keep the real `DEVICE_INVENTORY.md` local. This template intentionally uses generic identities and the [RFC 5737](https://datatracker.ietf.org/doc/html/rfc5737) documentation address range.

Last verified: **YYYY-MM-DD HH:MM TZ**

The device network is `<private subnet>`. It is reached through `<local network or VPN>`.

## Recorded devices

| Device | Hostname | Network location | Operating system | Hardware | Confirmed access | Physical location |
| --- | --- | --- | --- | --- | --- | --- |
| Local controller | `localhost` | `127.0.0.1` | `<OS and version>` | `<general hardware description>` | Local process | `<location>` |
| Linux worker | `worker.example.invalid` | `192.0.2.10` | `<OS and version>` | `<general hardware description>` | SSH `22/tcp` | `<location>` |
| GPU worker | `gpu.example.invalid` | `192.0.2.11` | `<OS and version>` | `<general hardware description>` | SSH `22/tcp` | `<location>` |
| Infrastructure console | `console.example.invalid` | `192.0.2.20` | `<OS and version>` | `<general hardware description>` | HTTPS | `<location>` |

## Discovery notes

- Record how devices were verified without including credentials.
- Avoid serial numbers, asset tags, personal names, public IP addresses, or precise physical locations.
- Keep real hostnames, private addresses, usernames, VPN names, and remote-access details in the ignored local file.

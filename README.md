# maxmind-geolite2-textfile-go
Fetch MaxMind's GeoLite2 country data and generate an IP blocklist using Go

## Prerequisites
To use the provided `Makefile`, `make` must also be installed.

## Building

```bash
make build
```

## Installation
Use the provided `Makefile` to install the binary and enable the `systemd` service and timer:

```bash
sudo make install
```

The service is enabled but not started immediately, meaning the blocklist won't be created until the timer triggers.

To build the blocklist immediately, manually start the service with:

```bash
sudo systemctl start blgen.service
```

You can also run the script manually from anywhere:

```bash
./blgen [options]
```

## Uninstallation
To remove the service, timer, and related files:

```bash
sudo make uninstall
```

## Usage

```
Usage: ./blgen [options]
  -bc value
    	ISO Country codes to block (can be used multiple times)
  -bn value
    	MaxMind continent codes to block (can be used multiple times)
  -c string
    	Config file
  -id string
    	Account ID
  -key string
    	License key
  -outname string
    	Output file (default "BlockedCountriesBlocks.txt")
  -outpath string
    	Output path
```

## Disclaimer
The authors of this script are not affiliated with MaxMind nd are providing the script as a convenient wrapper for integrating the publicly available list.

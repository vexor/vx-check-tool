all:
	GOOS=linux gom build

sha:
	shasum -a256 vx-systemd-check

bump:
	scripts/bump.sh

release:
	scripts/release.sh

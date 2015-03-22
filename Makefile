all:
	GOOS=linux gom build

bump:
	scripts/bump.sh

release:
	scripts/release.sh

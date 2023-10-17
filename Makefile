fuzz:
	git clone https://github.com/skip-mev/block-sdk.git || true
	cd fuz; go test -fuzz .

all: fuzz

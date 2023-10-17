fuzz:
	cd fuz; go test -fuzz .

all: fuzz

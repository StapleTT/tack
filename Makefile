MCC := third_party/moonlight-common-c

.PHONY: all mcc bridge test clean

all: bridge

# Static moonlight-common-c + enet, linked into the bridge through cgo.
mcc: build/mcc/libmoonlight-common-c.a

build/mcc/libmoonlight-common-c.a:
	git submodule update --init --recursive
	cmake -S $(MCC) -B build/mcc -DBUILD_SHARED_LIBS=OFF -DCMAKE_BUILD_TYPE=Release
	cmake --build build/mcc -j

bridge: mcc
	go build -o build/tack-bridge ./bridge/cmd/tack-bridge

test: mcc
	go test ./...

clean:
	rm -rf build

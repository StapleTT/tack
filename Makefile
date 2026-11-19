MCC := third_party/moonlight-common-c

.PHONY: all mcc web bridge agent test clean

all: bridge agent

# Static moonlight-common-c + enet, linked into the bridge through cgo.
mcc: build/mcc/libmoonlight-common-c.a

build/mcc/libmoonlight-common-c.a:
	git submodule update --init --recursive
	cmake -S $(MCC) -B build/mcc -DBUILD_SHARED_LIBS=OFF -DCMAKE_BUILD_TYPE=Release
	cmake --build build/mcc -j

web:
	cd web && npm install --silent && npx tsc

bridge: mcc web
	go build -o build/tack-bridge ./bridge/cmd/tack-bridge

# Pure Go, no cgo: builds anywhere, runs on the gaming PC.
agent:
	go build -o build/tack-agent ./agent

test: mcc web
	go test ./...

clean:
	rm -rf build web/dist

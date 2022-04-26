#!/bin/sh

git clone https://github.com/spruce-solutions/RandomX.git
cd RandomX
mkdir build
cd build
cmake -DARCH=native ..
make && cp librandomx.a ../../lib/
cp ../src/randomx.h ../../lib

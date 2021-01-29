all: package

libjfs/target/libjfs.so.gz: ../../pkg/*/*.go libjfs/*.go
	make -C libjfs

compile:
	mvn compile -B --quiet
test: libjfs
	mvn test -B --quiet
package: libjfs/target/libjfs.so.gz
	mvn package -B -Dmaven.test.skip=true

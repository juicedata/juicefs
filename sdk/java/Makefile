all: package

libjfs: ../../libjfs/target/libjfs.so.gz
	make -C ../libjfs

compile:
	mvn compile -B --quiet
test: libjfs
	mvn test -B --quiet
package:
	mvn package -B -Dmaven.test.skip=true --quiet

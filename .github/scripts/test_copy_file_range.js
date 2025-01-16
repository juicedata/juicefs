const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

if (process.argv.length !== 4) {
  console.error('Usage: node copyFile.js <sourceFile> <destinationFile>');
  process.exit(1);
}

const sourceFile = path.resolve(process.argv[2]);
const destinationFile = path.resolve(process.argv[3]);

function calculateHash(filePath) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash('sha256');
    const stream = fs.createReadStream(filePath);
    stream.on('data', (data) => hash.update(data));
    stream.on('end', () => resolve(hash.digest('hex')));
    stream.on('error', (err) => reject(err));
  });
}

fs.copyFile(sourceFile, destinationFile, async (err) => {
  if (err) {
    console.error('Error copying file:', err);
    process.exit(1);
  }
  console.log('File copied successfully.');
  try {
    const [sourceHash, destHash] = await Promise.all([
      calculateHash(sourceFile),
      calculateHash(destinationFile),
    ]);
    if (sourceHash === destHash) {
      console.log('The contents of the source and destination files are identical.');
    } else {
      console.log('The contents of the source and destination files are different.');
      process.exit(1);
    }
  } catch (err) {
    console.error('Error calculating file hashes:', err);
    process.exit(1);
  }
});

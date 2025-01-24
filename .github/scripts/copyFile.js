const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

if (process.argv.length !== 4) {
  console.error('Usage: node copyFile.js <sourceFile> <destinationFile>');
  process.exit(1);
}

const sourceFile = path.resolve(process.argv[2]);
const destinationFile = path.resolve(process.argv[3]);

fs.copyFile(sourceFile, destinationFile, async (err) => {
  if (err) {
    console.error('Error copying file:', err);
    process.exit(1);
  }
  console.log('File copied successfully.');
});
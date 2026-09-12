#!/usr/bin/env bash
set -e

npm init -y
npm install @noble/post-quantum @scure/bip39 esbuild

cat << 'EOF' > entry.js
import { ml_kem768 } from '@noble/post-quantum/ml-kem.js';
import { ml_dsa65 } from '@noble/post-quantum/ml-dsa.js';
import * as bip39 from '@scure/bip39';
import { wordlist } from '@scure/bip39/wordlists/english.js';

// Expose variables globally on window
window.CryptoEngine = {
  ml_kem768,
  ml_dsa65,
  randomBytes: (len) => crypto.getRandomValues(new Uint8Array(len)),
  bip39,
  wordlist
};
EOF

mkdir -p dist
npx esbuild entry.js --bundle --format=iife --outfile=dist/pqc-vault.js --minify

echo "Built dist/pqc-vault.js — copy it next to pqc-vault.html before opening it."

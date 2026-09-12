# Saving the ENCRYPTED CIPHER BUNDLE
1. Save the ENCRYPTED_CIPHER_BUNDLE as text file under heads /qubit_karma/bitwarden
2. Update the .bashrc:

      alias bitw='cat ~/qubit_karma/bitwarden'
3. LLM: .... npx esbuild entry.js --bundle --format=iife --outfile=dist/pqc-vault.js and use the static pqc-vault.js to run the application modified code for that

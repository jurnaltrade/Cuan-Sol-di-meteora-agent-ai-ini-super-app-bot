import fs from 'fs';
import crypto from 'crypto';

// Fungsi ini bisa Anda gunakan di dalam backend/sistem Anda
export function decryptWallet(encPath, password) {
    if (!password) {
        throw new Error("Password tidak diberikan");
    }

    const encData = fs.readFileSync(encPath);
    
    // Format: IV (12 bytes) + AuthTag (16 bytes) + Encrypted Data (sisanya)
    const iv = encData.subarray(0, 12);
    const authTag = encData.subarray(12, 28);
    const encrypted = encData.subarray(28);

    const key = crypto.createHash('sha256').update(String(password)).digest();

    const decipher = crypto.createDecipheriv('aes-256-gcm', key, iv);
    decipher.setAuthTag(authTag);

    let decrypted = decipher.update(encrypted);
    decrypted = Buffer.concat([decrypted, decipher.final()]);

    return decrypted; // Mengembalikan Buffer yang bisa diubah menjadi JSON string
}

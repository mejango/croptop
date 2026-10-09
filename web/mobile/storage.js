const DATABASE = 'croptop-mobile-v1';
let opening;
const imageBuffers = new WeakMap();
function database() {
  if (!opening) opening = new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE, 1);
    request.onupgradeneeded = () => request.result.createObjectStore('state');
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(new Error('Local storage is unavailable. Your draft has not been saved.', { cause: request.error }));
    request.onblocked = () => reject(new Error('Close other Croptop tabs to open local storage.'));
  });
  return opening;
}
async function transaction(mode, action) {
  const db = await database();
  return new Promise((resolve, reject) => {
    const tx = db.transaction('state', mode);
    let result;
    tx.oncomplete = () => resolve(result);
    tx.onerror = tx.onabort = () => reject(new Error('Your changes could not be saved on this phone. Free up browser storage and retry.', { cause: tx.error }));
    action(tx.objectStore('state'), value => { result = value; });
  });
}
export async function read(key) {
  const value = await transaction('readonly', (store, done) => { const request = store.get(key); request.onsuccess = () => done(request.result); });
  if (key.startsWith('draft') && value?.image?.data instanceof ArrayBuffer) {
    const { data, name, type, lastModified } = value.image;
    value.image = new File([data], name, { type, lastModified });
    imageBuffers.set(value.image, Promise.resolve(data));
  }
  return value;
}
export async function save(key, value) {
  if (key.startsWith('draft') && value?.image instanceof Blob) {
    // ArrayBuffer avoids WebKit's temporary file-backed Blob serialization
    // failure. Read before opening the transaction; no async gaps inside it.
    const image = value.image;
    if (!imageBuffers.has(image)) imageBuffers.set(image, image.arrayBuffer());
    value = { ...value, image: { data: await imageBuffers.get(image), name: image.name || 'image', type: image.type, lastModified: image.lastModified || Date.now() } };
  }
  return transaction('readwrite', store => { store.put(value, key); });
}
export const remove = key => transaction('readwrite', store => { store.delete(key); });

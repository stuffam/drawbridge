/** Saves a blob as a file, through the browser's download. */
export function saveBlob(name: string, blob: Blob) {
	const url = URL.createObjectURL(blob);
	const a = document.createElement('a');
	a.href = url;
	a.download = name;
	// Some browsers only download from a link in the document, and cancel a download
	// whose URL is revoked at once.
	document.body.append(a);
	a.click();
	a.remove();
	setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** Saves text as a file, through the browser's download. */
export function saveText(name: string, text: string, type = 'text/plain') {
	saveBlob(name, new Blob([text], { type }));
}

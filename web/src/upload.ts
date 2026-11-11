// upload sends a file with optional JSON meta and reports progress.
export function upload(path: string, file: File, meta: object | null, onProgress: (p: number) => void): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const body = new FormData()
    if (meta) body.append('meta', JSON.stringify(meta))
    body.append('file', file)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/v1' + path)
    xhr.setRequestHeader('X-Requested-With', 'taktaplus')
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(Math.round((e.loaded / e.total) * 100))
    xhr.onload = () => {
      let data: { error?: string } = {}
      try { data = JSON.parse(xhr.responseText) } catch { /* not JSON */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data)
      else reject(new Error(data.error ?? xhr.statusText))
    }
    xhr.onerror = () => reject(new Error('ارسال فایل ناموفق بود'))
    xhr.send(body)
  })
}

import { useState } from 'react'
import { fetchMedia } from '../api/client'
import { useApi } from '../hooks/useApi'
import Pagination from './Pagination'

export default function MediaGallery() {
  const [page, setPage] = useState(1);

  const { data, loading, error } = useApi(
    () => fetchMedia({ page, per_page: 12, type: 'image', include: 'variants' }),
    [page]
  );

  if (loading) return <div className="loading">Loading media...</div>;
  if (error) return <div className="error">{error}</div>;
  if (!data || data.data.length === 0) {
    return (
      <div className="empty">
        <h2>No media found</h2>
        <p>Upload some images in the oCMS admin panel.</p>
      </div>
    );
  }

  return (
    <div>
      <h1>Media Gallery</h1>
      <div className="media-grid">
        {data.data.map(item => (
          <div key={item.id} className="media-card">
            <div className="media-image">
              <img
                src={item.urls.medium || item.urls.thumbnail || item.urls.original}
                alt={item.alt || item.filename}
                loading="lazy"
              />
            </div>
            <div className="media-info">
              <p className="media-filename">{item.filename}</p>
              <p className="media-meta">
                {item.width}x{item.height} &middot; {formatFileSize(item.size)}
              </p>
            </div>
          </div>
        ))}
      </div>
      <Pagination meta={data.meta} onPageChange={setPage} />
    </div>
  );
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

import { useState } from 'react'
import { Link } from 'react-router'
import { fetchPages } from '../api/client'
import { useApi } from '../hooks/useApi'
import Pagination from './Pagination'

export default function PageList() {
  const [page, setPage] = useState(1);

  const { data, loading, error } = useApi(
    () => fetchPages({ page, per_page: 10, include: 'author,tags' }),
    [page]
  );

  if (loading) return <div className="loading">Loading pages...</div>;
  if (error) return <div className="error">{error}</div>;
  if (!data || data.data.length === 0) {
    return (
      <div className="empty">
        <h2>No pages found</h2>
        <p>Create some content in the oCMS admin panel to see it here.</p>
      </div>
    );
  }

  return (
    <div>
      <h1>Pages</h1>
      <div className="page-list">
        {data.data.map(p => (
          <article key={p.id} className="page-card">
            <Link to={`/page/${p.slug}`}>
              <h2>{p.title}</h2>
            </Link>
            <div className="page-meta">
              {p.author && <span>By {p.author.display_name}</span>}
              {p.published_at && (
                <time dateTime={p.published_at}>
                  {new Date(p.published_at).toLocaleDateString()}
                </time>
              )}
            </div>
            {p.meta_description && (
              <p className="page-excerpt">{p.meta_description}</p>
            )}
            {p.tags && p.tags.length > 0 && (
              <div className="tag-list">
                {p.tags.map(t => (
                  <span key={t.id} className="tag">{t.name}</span>
                ))}
              </div>
            )}
          </article>
        ))}
      </div>
      <Pagination meta={data.meta} onPageChange={setPage} />
    </div>
  );
}

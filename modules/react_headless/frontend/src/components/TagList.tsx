import { fetchTags } from '../api/client'
import { useApi } from '../hooks/useApi'

export default function TagList() {
  const { data, loading, error } = useApi(
    () => fetchTags({ per_page: 100 }),
    []
  );

  if (loading) return <div className="loading">Loading tags...</div>;
  if (error) return <div className="error">{error}</div>;
  if (!data || data.data.length === 0) {
    return (
      <div className="empty">
        <h2>No tags found</h2>
        <p>Create some tags in the oCMS admin panel.</p>
      </div>
    );
  }

  return (
    <div>
      <h1>Tags</h1>
      <div className="tag-grid">
        {data.data.map(tag => (
          <div key={tag.id} className="tag-card">
            <h3>{tag.name}</h3>
            <span className="tag-count">{tag.page_count} pages</span>
            <code className="tag-slug">/{tag.slug}</code>
          </div>
        ))}
      </div>
    </div>
  );
}

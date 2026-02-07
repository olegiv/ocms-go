import { Routes, Route } from 'react-router-dom'
import Layout from './components/Layout'
import PageList from './components/PageList'
import PageDetail from './components/PageDetail'
import MediaGallery from './components/MediaGallery'
import TagList from './components/TagList'
import CategoryTree from './components/CategoryTree'

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<PageList />} />
        <Route path="/page/:slug" element={<PageDetail />} />
        <Route path="/media" element={<MediaGallery />} />
        <Route path="/tags" element={<TagList />} />
        <Route path="/categories" element={<CategoryTree />} />
      </Route>
    </Routes>
  )
}

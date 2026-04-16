import { BrowserRouter, Routes, Route, useParams } from 'react-router-dom'
import { SetupScreen } from './components/SetupScreen'
import { TradingView } from './components/TradingView'
import { PositionsPage } from './components/PositionsPage'

function TradingViewRoute() {
  const { symbol, exchangeA, exchangeB } = useParams<{
    symbol: string
    exchangeA: string
    exchangeB: string
  }>()
  if (!symbol || !exchangeA || !exchangeB) return <div>Invalid URL</div>
  return <TradingView symbol={symbol} exchangeA={exchangeA} exchangeB={exchangeB} />
}

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<SetupScreen />} />
        <Route path="/trade/:symbol/:exchangeA/:exchangeB" element={<TradingViewRoute />} />
        <Route path="/positions" element={<PositionsPage />} />
      </Routes>
    </BrowserRouter>
  )
}

export default App

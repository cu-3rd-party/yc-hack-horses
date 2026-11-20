import { Route, Routes } from 'react-router-dom';
import { Sidebar } from '../widgets/sidebar/sidebar';
import { DashboardPage } from '../pages/dashboard/dashboard-page';
import { HorsesPage } from '../pages/horses/horses-page';
import { ObservationsPage } from '../pages/observations/observations-page';
import { NewObservationPage } from '../pages/new-observation/new-observation-page';
import { SyncPage } from '../pages/sync/sync-page';
import { SettingsPage } from '../pages/settings/settings-page';

export default function App() {
  return (
    <div className="flex h-screen">
      <Sidebar />
      <main className="flex-1 overflow-y-auto p-6">
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/horses" element={<HorsesPage />} />
          <Route path="/observations" element={<ObservationsPage />} />
          <Route path="/new-observation" element={<NewObservationPage />} />
          <Route path="/sync" element={<SyncPage />} />
          <Route path="/settings" element={<SettingsPage />} />
        </Routes>
      </main>
    </div>
  );
}

import { registerRootComponent } from 'expo';

import App from './src/App';

// registerRootComponent гарантирует корректный startup и в Expo Go, и в dev client.
registerRootComponent(App);

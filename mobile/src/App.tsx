import React from 'react';
import { NavigationContainer } from '@react-navigation/native';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { StatusBar } from 'expo-status-bar';
import { useEffect, useRef } from 'react';
import { StyleSheet, Text, View } from 'react-native';

import { DashboardScreen } from './screens/DashboardScreen';
import { ObservationsScreen } from './screens/ObservationsScreen';
import { NewObservationScreen } from './screens/NewObservationScreen';
import { SyncScreen } from './screens/SyncScreen';
import { goCore } from './shared/go-core/bridge';
import { appDataDir } from './shared/go-core/data-dir';
import { FOREGROUND_SYNC_PEER } from './shared/config/sync';

export type RootStackParamList = {
  Dashboard: undefined;
  Observations: undefined;
  NewObservation: undefined;
  Sync: undefined;
};

const Stack = createNativeStackNavigator<RootStackParamList>();

// Автозапуск foreground sync при открытии приложения (ADR-0011: без фона).
// useRef защищает от повторного запуска при ремоунте навигации.
export default function App() {
  const syncStarted = useRef(false);

  useEffect(() => {
    if (!goCore.available) return;
    if (syncStarted.current) return;
    syncStarted.current = true;
    goCore
      .init(appDataDir())
      .then(() => goCore.foregroundSync(FOREGROUND_SYNC_PEER))
      .catch((err: unknown) => {
        // автосинк не критичен: ошибка видна на экране Sync при ручном прогоне
        console.warn('foreground sync on start failed:', err);
      });
  }, []);

  if (!goCore.available) {
    // Не заглушка: честное состояние сборки. Expo Go не содержит gomobile-библиотеку.
    return (
      <View style={styles.warning}>
        <StatusBar style="auto" />
        <Text style={styles.warningTitle}>Go Core не вшит в сборку</Text>
        <Text style={styles.warningText}>
          Нужен dev client с библиотекой backend/cmd/mobilebind (gomobile bind).
          Сборка и проверка — в mobile/README.md.
        </Text>
      </View>
    );
  }

  return (
    <NavigationContainer>
      <StatusBar style="auto" />
      <Stack.Navigator initialRouteName="Dashboard">
        <Stack.Screen name="Dashboard" component={DashboardScreen} />
        <Stack.Screen name="Observations" component={ObservationsScreen} />
        <Stack.Screen name="NewObservation" component={NewObservationScreen} />
        <Stack.Screen name="Sync" component={SyncScreen} />
      </Stack.Navigator>
    </NavigationContainer>
  );
}

const styles = StyleSheet.create({
  warning: { flex: 1, alignItems: 'center', justifyContent: 'center', padding: 24, gap: 12 },
  warningTitle: { fontSize: 18, fontWeight: '600' },
  warningText: { textAlign: 'center', color: '#666' },
});

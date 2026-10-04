import type { z } from 'zod';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import {
  GeodataScheduleViewSchema,
  XrayRestartRunSchema,
  XrayRestartScheduleViewSchema,
} from '@/generated/zod';
import type { GeodataUpdateSchedule, XrayRestartSchedule } from '@/generated/types';
import { HttpUtil, type Msg } from '@/utils';
import { parseMsg } from '@/utils/zodValidate';
import { keys } from '@/api/queryKeys';

const JSON_OPTIONS = { silent: true, headers: { 'Content-Type': 'application/json' } };

function result<T>(msg: Msg, schema: z.ZodType<T>, context: string): T {
  if (!msg.success) throw new Error(msg.msg || 'Request failed');
  const validated = parseMsg(msg, schema, context, { strict: true });
  if (validated.obj == null) throw new Error(`${context} response failed validation`);
  return validated.obj;
}

async function getRestartSchedule() {
  return result(
    await HttpUtil.get('/panel/api/xray/schedule/restart', undefined, { silent: true }),
    XrayRestartScheduleViewSchema,
    'xray/schedule/restart',
  );
}

async function saveRestartSchedule(config: XrayRestartSchedule) {
  return result(
    await HttpUtil.post('/panel/api/xray/schedule/restart', config, JSON_OPTIONS),
    XrayRestartScheduleViewSchema,
    'xray/schedule/restart',
  );
}

async function runRestartSchedule() {
  return result(
    await HttpUtil.post('/panel/api/xray/schedule/restart/run', undefined, JSON_OPTIONS),
    XrayRestartRunSchema,
    'xray/schedule/restart/run',
  );
}

async function getGeodataSchedule() {
  return result(
    await HttpUtil.get('/panel/api/xray/schedule/geodata', undefined, { silent: true }),
    GeodataScheduleViewSchema,
    'xray/schedule/geodata',
  );
}

async function saveGeodataSchedule(config: GeodataUpdateSchedule) {
  return result(
    await HttpUtil.post('/panel/api/xray/schedule/geodata', config, JSON_OPTIONS),
    GeodataScheduleViewSchema,
    'xray/schedule/geodata',
  );
}

export function useXrayRestartSchedule() {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: keys.xray.schedule.restart(),
    queryFn: getRestartSchedule,
    refetchInterval: 10000,
    retry: false,
  });
  const save = useMutation({
    mutationFn: saveRestartSchedule,
    onSuccess: (next) => {
      queryClient.setQueryData(keys.xray.schedule.restart(), next);
      void queryClient.invalidateQueries({ queryKey: keys.xray.schedule.restart() });
    },
  });
  const run = useMutation({
    mutationFn: runRestartSchedule,
    onSettled: () => queryClient.invalidateQueries({ queryKey: keys.xray.schedule.restart() }),
  });
  return { query, save, run };
}

export function useGeodataUpdateSchedule(active: boolean) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: keys.xray.schedule.geodata(),
    queryFn: getGeodataSchedule,
    enabled: active,
    retry: false,
  });
  const save = useMutation({
    mutationFn: saveGeodataSchedule,
    onSuccess: (next) => {
      queryClient.setQueryData(keys.xray.schedule.geodata(), next);
      void queryClient.invalidateQueries({
        queryKey: keys.xray.schedule.geodata(),
        refetchType: 'none',
      });
      void queryClient.invalidateQueries({ queryKey: keys.xray.config() });
    },
  });
  return { query, save };
}

import importlib.util
from importlib.machinery import SourceFileLoader
from pathlib import Path
import unittest

loader = SourceFileLoader('duty', str(Path(__file__).with_name('gc-duty-reconcile')))
spec = importlib.util.spec_from_loader('duty', loader)
duty = importlib.util.module_from_spec(spec)
spec.loader.exec_module(duty)


class Duty(unittest.TestCase):
    def fixtures(self):
        target = {'session': 'duty', 'provider': 'managed', 'message': 'Reconcile durable work.', 'allow_without_work': True}
        row = {'id': 'gc-one', 'state': 'active', 'attached': False, 'session_name': 'runtime'}
        bead = {'id': 'gc-one', 'metadata': {'provider': 'managed', 'state': 'active', 'continuation_epoch': '1', 'instance_token': 'token'}}
        queue = {'session_id': 'gc-one', 'counts': {'pending': 0, 'in_flight': 0, 'dead': 0, 'blocked': 0}}
        host = {'ready': True, 'provider_session_id': 'native', 'fence': {'session_id': 'gc-one', 'continuation_epoch': '1', 'runtime_token': 'token'}, 'pending_count': 0, 'last_receipt': {'state': 'accepted', 'terminal': 'failed'}}
        return target, row, bead, queue, host

    def test_active_terminal_failure_can_reconcile_after_cadence(self):
        self.assertIsNone(duty.skip_reason(*self.fixtures(), {}, 2000, 1800))

    def test_holds_attachment_and_pending_skip(self):
        for field in ('held_until', 'wait_hold', 'quarantined_until', 'sleep_intent'):
            parts = self.fixtures()
            parts[2]['metadata'][field] = 'held'
            self.assertEqual(duty.skip_reason(*parts, {}, 2000, 1800), 'held')
        parts = self.fixtures(); parts[1]['attached'] = True
        self.assertEqual(duty.skip_reason(*parts, {}, 2000, 1800), 'inactive_or_attached')
        parts = self.fixtures(); parts[3]['counts']['blocked'] = 1
        self.assertEqual(duty.skip_reason(*parts, {}, 2000, 1800), 'queue_pending')
        parts = self.fixtures(); parts[3]['counts'].pop('blocked'); parts[3]['counts']['dead'] = 1
        self.assertEqual(duty.skip_reason(*parts, {}, 2000, 1800), 'queue_dead')
        parts = self.fixtures(); parts[3]['counts'].pop('dead')
        self.assertEqual(duty.skip_reason(*parts, {}, 2000, 1800), 'queue_unknown')
        parts = self.fixtures(); parts[3]['counts'].pop('blocked')
        self.assertIsNone(duty.skip_reason(*parts, {}, 2000, 1800))

    def test_unknown_or_busy_never_periodically_retries(self):
        parts = self.fixtures(); parts[4]['pending_count'] = 1
        self.assertEqual(duty.skip_reason(*parts, {}, 2000, 1800), 'provider_pending')
        self.assertEqual(duty.skip_reason(*self.fixtures(), {'outcome': 'unknown'}, 2000, 1800), 'prior_enqueue_unknown')
        self.assertEqual(duty.skip_reason(*self.fixtures(), {'outcome': 'queued', 'at': 1900}, 2000, 1800), 'cooldown')

    def test_idle_without_explicit_permanent_duty_skips(self):
        parts = self.fixtures(); parts[0]['allow_without_work'] = False
        self.assertEqual(duty.skip_reason(*parts, {}, 2000, 1800), 'no_work')

    def test_enqueue_timeout_pins_before_call_and_never_retries(self):
        import subprocess
        target, row, bead, queue, host = self.fixtures()
        row['alias'] = 'duty'
        def query(args):
            if args[0] == 'session': return {'sessions': [row]}
            if args[0] == 'beads': return {'bead': bead}
            if args[0] == 'nudge': return queue
            return host
        state, saved = {}, []
        def enqueue(args, request):
            self.assertEqual(state['gc-one']['outcome'], 'unknown')
            self.assertEqual(args, ['@msp', 'conditional-admit', 'runtime'])
            self.assertEqual(state['gc-one']['request'], request)
            raise subprocess.TimeoutExpired('gc', 10)
        config = {'targets': [target]}
        result = duty.reconcile(config, state, lambda x: saved.append(x.copy()), query, enqueue, 2000)
        self.assertEqual(result[0]['outcome'], 'unknown')
        result = duty.reconcile(config, state, lambda _: None, query, lambda *_: self.fail('retried unknown'), 4000)
        self.assertEqual(result[0]['outcome'], 'prior_enqueue_unknown')
        self.assertEqual(len(saved), 1)

    def test_alias_and_id_share_one_cadence(self):
        target, row, bead, queue, host = self.fixtures()
        row['alias'] = 'duty'
        def query(args):
            if args[0] == 'session': return {'sessions': [row]}
            if args[0] == 'beads': return {'bead': bead}
            if args[0] == 'nudge': return queue
            return host
        sent = []
        def enqueue(args, request):
            sent.append(args)
            return dict(request, state='accepted', receipt_id='native:11')
        second = dict(target, session=row['id'])
        result = duty.reconcile({'targets': [target, second]}, {}, lambda _: None, query, enqueue, 2000)
        self.assertEqual([r['outcome'] for r in result], ['accepted', 'cooldown'])
        self.assertEqual(len(sent), 1)

    def test_admission_ack_does_not_claim_execution(self):
        request = {'command_id': 'command', 'provider_session_id': 'native', 'fence': {'session_id': 'gc-one'}}
        self.assertTrue(duty.admission_ack(dict(request, state='accepted', receipt_id='native:11'), request))
        self.assertFalse(duty.admission_ack(dict(request, command_id='other', state='accepted', receipt_id='native:11'), request))
        self.assertFalse(duty.admission_ack(dict(request, state='unknown'), request))


if __name__ == '__main__':
    unittest.main()

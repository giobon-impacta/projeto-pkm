import test from 'node:test';
import assert from 'node:assert/strict';
import { layoutGraph } from './graph-view.js';

test('lays out notes and counts their valid connections', () => {
    const graph = layoutGraph(
        [
            { id: 1, title: 'First', folder: '' },
            { id: 2, title: 'Second', folder: 'Projects' },
            { id: 3, title: 'Third', folder: '' }
        ],
        [
            { source: 1, target: 2, type: 'markdown' },
            { source: 2, target: 3, type: 'wikilink' },
            { source: 1, target: 99, type: 'markdown' }
        ],
        800,
        600
    );

    assert.equal(graph.nodes.length, 3);
    assert.equal(graph.edges.length, 2);
    assert.deepEqual(graph.nodes.map(node => node.degree), [1, 2, 1]);
    for (const node of graph.nodes) {
        assert.ok(Number.isFinite(node.x));
        assert.ok(Number.isFinite(node.y));
        assert.ok(node.x >= 80 && node.x <= 720);
        assert.ok(node.y >= 60 && node.y <= 540);
    }
});

test('uses stable positions for the same graph and viewport', () => {
    const nodes = [{ id: 10, title: 'Note', folder: '' }];
    const first = layoutGraph(nodes, [], 700, 500);
    const second = layoutGraph(nodes, [], 700, 500);

    assert.equal(first.nodes[0].x, second.nodes[0].x);
    assert.equal(first.nodes[0].y, second.nodes[0].y);
});

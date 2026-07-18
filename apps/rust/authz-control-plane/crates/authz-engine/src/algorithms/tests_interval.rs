#[cfg(test)]
mod tests {
    use crate::algorithms::interval_tree::{TemporalInterval, TemporalIntervalTree};

    #[test]
    fn test_interval_overlaps() {
        let mut tree = TemporalIntervalTree::new();
        // Policy A: Jan 1 -> Jan 10
        tree.insert(TemporalInterval::new(10, 100));
        // Policy B: Jan 5 -> Jan 15
        tree.insert(TemporalInterval::new(50, 150));
        // Policy C: Jan 12 -> Jan 20
        tree.insert(TemporalInterval::new(120, 200));

        // Query: Jan 8 -> Jan 11 (Overlaps A and B, but not C)
        let overlaps = tree.find_overlapping(TemporalInterval::new(80, 110));
        assert_eq!(overlaps.len(), 2);
        assert!(overlaps.contains(&TemporalInterval::new(10, 100)));
        assert!(overlaps.contains(&TemporalInterval::new(50, 150)));

        // Query: Jan 1 -> Jan 4 (Overlaps A only)
        let overlaps = tree.find_overlapping(TemporalInterval::new(10, 40));
        assert_eq!(overlaps.len(), 1);
        assert!(overlaps.contains(&TemporalInterval::new(10, 100)));

        // Query: Jan 21 -> Jan 25 (No overlap)
        let overlaps = tree.find_overlapping(TemporalInterval::new(210, 250));
        assert_eq!(overlaps.len(), 0);
    }

    #[test]
    fn test_interval_point_query() {
        let mut tree = TemporalIntervalTree::new();
        tree.insert(TemporalInterval::new(10, 20));
        tree.insert(TemporalInterval::new(15, 25));
        tree.insert(TemporalInterval::new(30, 40));

        // Point query at 15
        let overlaps = tree.find_overlapping(TemporalInterval::new(15, 15));
        assert_eq!(overlaps.len(), 2);
        assert!(overlaps.contains(&TemporalInterval::new(10, 20)));
        assert!(overlaps.contains(&TemporalInterval::new(15, 25)));
    }
}
